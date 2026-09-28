package dev.servercontrolpanel.data.auth

import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

private const val EXPIRY_SKEW_MILLIS = 60_000L

sealed interface SessionState {

    data object SignedOut : SessionState

    data class SignedIn(val expiresAtEpochMillis: Long) : SessionState
}

sealed interface RefreshOutcome {

    data class Renewed(val accessToken: String, val refreshToken: String, val expiresInSeconds: Long) : RefreshOutcome

    data object Rejected : RefreshOutcome

    data object Unavailable : RefreshOutcome
}

interface SessionRefresher {
    suspend fun refresh(refreshToken: String): RefreshOutcome
}

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

    val state: StateFlow<SessionState> = _state.asStateFlow()

    init {
        adopt(tokenStore.load(), persist = false)
    }

    val isSessionPersistent: Boolean get() = tokenStore.isPersistent

    fun currentAccessToken(): String? = tokens?.accessToken

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

    fun signOut() {
        adopt(null, persist = true)
    }

    suspend fun accessTokenForRequest(): String? {
        val current = tokens ?: return null
        if (current.expiresAtEpochMillis - EXPIRY_SKEW_MILLIS > now()) return current.accessToken
        return renew(staleToken = current.accessToken)
    }

    suspend fun refreshAfterUnauthorized(staleToken: String?): String? = renew(staleToken)

    private suspend fun renew(staleToken: String?): String? = refreshMutex.withLock {
        val current = tokens ?: return null
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
            RefreshOutcome.Rejected -> {
                adopt(null, persist = true)
                null
            }
            RefreshOutcome.Unavailable -> null
        }
    }

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
