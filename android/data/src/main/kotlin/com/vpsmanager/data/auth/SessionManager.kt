package com.vpsmanager.data.auth

import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Margin applied to the access token's expiry in [SessionManager.accessTokenForRequest].
 * Refreshing exactly on the second of expiry loses the race against the
 * request's own network time (and against any clock drift between device and
 * server); 60s is enough slack for a normal BFF call without turning the
 * refresh into a loop.
 */
private const val EXPIRY_SKEW_MILLIS = 60_000L

/**
 * This device's session state, observable by the UI. It decides the first
 * screen (see `LaunchDecisions` in `:app`) and it is what makes a 401 lead back
 * to login instead of becoming a "Try again" card that will never work.
 */
sealed interface SessionState {

    /** No session: a login is required (passkey or password). */
    data object SignedOut : SessionState

    /** There is a usable access+refresh pair. */
    data class SignedIn(val expiresAtEpochMillis: Long) : SessionState
}

/** The result of a refresh attempt — see [SessionRefresher]. */
sealed interface RefreshOutcome {

    data class Renewed(val accessToken: String, val refreshToken: String, val expiresInSeconds: Long) : RefreshOutcome

    /**
     * The server refused the refresh token (401/403): it was rotated, revoked
     * or expired. There is no possible second attempt — the session is dead and
     * the app has to go back to login.
     */
    data object Rejected : RefreshOutcome

    /**
     * Could not be determined (network failure, 5xx). Deliberately distinct
     * from [Rejected]: tearing down the operator's session because the Wi-Fi
     * dropped for three seconds would trade a transient failure for a mandatory
     * login. The request that prompted the refresh returns its original 401 and
     * the screen shows the error; nothing goes into a loop.
     */
    data object Unavailable : RefreshOutcome
}

/**
 * Exchanges a refresh token for a new pair. An interface (rather than the
 * concrete class) so that [SessionManager]'s tests need no network — the
 * production implementation is [BffSessionRefresher].
 */
interface SessionRefresher {
    suspend fun refresh(refreshToken: String): RefreshOutcome
}

/**
 * The device's session: it holds the token pair, publishes the access token to
 * whoever makes requests, and refreshes in a SINGLE QUEUE.
 *
 * ## The single refresh queue
 * Several screens load at once (Home, events, WhatsApp…). When the access token
 * expires, they all take a 401 almost simultaneously. Without serialisation,
 * each would fire its own `POST /auth/refresh` — and the BFF's refresh is
 * ROTATING (`auth_login.go`: "the token you sent stops working on THIS call,
 * success or not"). The first refresh would invalidate the token the other N-1
 * had just sent, and the outcome of N concurrent refreshes would be a
 * torn-down session — exactly the opposite of what refreshing exists to
 * prevent.
 *
 * The mechanism is [refreshMutex] plus [refreshAfterUnauthorized]'s
 * `staleToken` parameter: whoever arrives later waits on the mutex and, on
 * entering, notices that the current token is no longer the one that took the
 * 401 and returns the new one WITHOUT refreshing again. One refresh per expiry,
 * not one per call.
 *
 * ## Mirroring into [ApiClient.accessToken]
 * [ApiClient] has a companion `accessToken` that
 * [com.vpsmanager.data.media.mediaCallFactory] and
 * [com.vpsmanager.data.whatsapp.OkHttpWhatsAppWsFactory] already read — and
 * that nobody ever wrote (which was why EVERY screen showed a 401). Every token
 * change goes through [publishAccessToken], so those two paths (media bytes via
 * Coil/Media3 and the WhatsApp socket) get the token through the same channel,
 * without either having to know about [SessionManager].
 */
class SessionManager(
    private val tokenStore: TokenStore,
    private val refresher: SessionRefresher,
    private val now: () -> Long = System::currentTimeMillis,
    private val publishAccessToken: (String?) -> Unit = { ApiClient.accessToken = it },
) {

    private val refreshMutex = Mutex()

    @Volatile
    private var tokens: SessionTokens? = null

    private val _state = MutableStateFlow<SessionState>(SessionState.SignedOut)

    /** Observed by `MainActivity` to choose between login and Home. */
    val state: StateFlow<SessionState> = _state.asStateFlow()

    init {
        // Load the persisted session (if any) right in the constructor: the
        // app decides its first screen from it. When the store has degraded to
        // memory (Keystore unavailable), this is always null — that is
        // KeystoreTokenStore's fail-closed behaviour working.
        adopt(tokenStore.load(), persist = false)
    }

    /** `false` when the session will not survive the next boot — see [TokenStore.isPersistent]. */
    val isSessionPersistent: Boolean get() = tokenStore.isPersistent

    /** The current access token, with no refresh attempted. */
    fun currentAccessToken(): String? = tokens?.accessToken

    /**
     * Records the pair just issued by a login (passkey or password) and moves
     * the app into the authenticated state.
     */
    fun establish(accessToken: String, refreshToken: String, expiresInSeconds: Long) {
        adopt(
            SessionTokens(
                accessToken = accessToken,
                refreshToken = refreshToken,
                expiresAtEpochMillis = now() + expiresInSeconds * 1_000L,
            ),
            persist = true,
        )
    }

    /**
     * Tears down the local session: wipes the store, clears the published token
     * and pushes the app back to login. It does not call `/auth/logout` —
     * revoking the session on the server is the job of someone holding a valid
     * token for it, and this path is precisely the one that runs when the token
     * is no longer valid.
     */
    fun signOut() {
        adopt(null, persist = true)
    }

    /**
     * The token to use on a request, refreshing BEFORE setting out if it has
     * already expired (or is within [EXPIRY_SKEW_MILLIS] of doing so). This is
     * the proactive path — the reactive one is [refreshAfterUnauthorized].
     */
    suspend fun accessTokenForRequest(): String? {
        val current = tokens ?: return null
        if (current.expiresAtEpochMillis - EXPIRY_SKEW_MILLIS > now()) return current.accessToken
        return renew(staleToken = current.accessToken)
    }

    /**
     * Called when a request took a 401 with [staleToken]. Returns a fresh token
     * so the call can be repeated, or `null` when there is no recoverable
     * session (in which case the caller returns the 401 and the UI leads to
     * login).
     */
    suspend fun refreshAfterUnauthorized(staleToken: String?): String? = renew(staleToken)

    /**
     * The body of the single queue. Everything — the "has someone already
     * refreshed?" check and the network call — happens INSIDE the mutex on
     * purpose: if the check sat outside, two calls could see the same stale
     * token and both would go in to refresh, which is exactly the race this
     * method exists to eliminate.
     */
    private suspend fun renew(staleToken: String?): String? = refreshMutex.withLock {
        val current = tokens ?: return null
        // Another call already refreshed while this one waited on the mutex:
        // the current token is no longer the one that took the 401. Reuse it —
        // one refresh per expiry, not one per call.
        if (staleToken != null && current.accessToken != staleToken) return current.accessToken

        when (val outcome = refresher.refresh(current.refreshToken)) {
            is RefreshOutcome.Renewed -> {
                adopt(
                    SessionTokens(
                        accessToken = outcome.accessToken,
                        refreshToken = outcome.refreshToken,
                        expiresAtEpochMillis = now() + outcome.expiresInSeconds * 1_000L,
                    ),
                    persist = true,
                )
                outcome.accessToken
            }
            // Refresh token dead: nothing to retry. The session is torn down
            // HERE (and not in the caller) so that any refresh path — proactive
            // or reactive — ends at login, never at a mute retry.
            RefreshOutcome.Rejected -> {
                adopt(null, persist = true)
                null
            }
            // Transient: keep the session as it is and return null. The
            // request that prompted this fails once; the operator's next
            // attempt (or the next screen) tries again.
            RefreshOutcome.Unavailable -> null
        }
    }

    /**
     * The SINGLE point of session change: memory, persistent store, the token
     * published in [ApiClient.accessToken] and [state] always move together.
     * Having one place is what prevents the classic failure mode here — a token
     * updated in memory but not in what the requests read.
     */
    private fun adopt(newTokens: SessionTokens?, persist: Boolean) {
        tokens = newTokens
        if (persist) {
            if (newTokens == null) tokenStore.clear() else tokenStore.save(newTokens)
        }
        publishAccessToken(newTokens?.accessToken)
        _state.value = newTokens
            ?.let { SessionState.SignedIn(it.expiresAtEpochMillis) }
            ?: SessionState.SignedOut
    }
}
