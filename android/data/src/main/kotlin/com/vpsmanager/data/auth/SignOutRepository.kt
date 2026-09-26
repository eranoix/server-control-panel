package com.vpsmanager.data.auth

import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.data.offline.CacheDeLeitura
import com.vpsmanager.mobileapiclient.api.AuthApi

/**
 * The slice the navigation shell depends on for the "Sign out" button — same
 * convention as [PasswordLoginSource]: anything outside `:data` tests against
 * a fake without touching the generated client.
 */
interface SignOutSource {
    suspend fun signOut()
}

/**
 * Really ends this device's session: revokes it on the server AND wipes the
 * local tokens.
 *
 * Until now the app had no WAY to sign out. `/auth/logout` existed in the BFF
 * and [SessionManager.signOut] existed in the app, and nobody called either of
 * them — the session only dropped when a renewal was refused. This class is
 * the half that was missing.
 *
 * ## Two halves, and why this order
 * 1. `POST /auth/logout` revokes **only the jti of this call's token**
 *    (`internal/mobilebff/auth_login.go`) — the same user's other sessions
 *    stay up. It has to happen BEFORE the tokens are wiped, because it is the
 *    current Bearer that authorises the revocation: `auth/logout` is
 *    deliberately OFF [AuthTokenInterceptor]'s public route list precisely so
 *    that it receives the header.
 * 2. [SessionManager.signOut] wipes the local guard, clears the token
 *    published in `ApiClient.accessToken` and pushes the state to
 *    [SessionState.SignedOut] — which is what returns the operator to the sign
 *    in screen, with no explicit navigation.
 *
 * ## The revocation is best effort; the local sign out is NOT
 * Step 2 runs in a `finally`. Signing out on a plane, with the server down, or
 * with an already expired token, has to go on signing out: a "Sign out" that
 * does not sign out because the network dropped would leave the session alive
 * on the device — exactly the opposite of what the operator asked for. The
 * endpoint is idempotent (calling it again with an already revoked token still
 * answers 200), so nothing is lost when the call fails and the token is
 * discarded anyway; what remains is a server-side session that expires by
 * itself.
 */
class SignOutRepository(
    private val session: SessionManager,
    private val serverConfigRepository: ServerConfigRepository,
    private val authApiFactory: (String) -> AuthApi = { basePath -> AuthApi(basePath) },
) : SignOutSource {

    override suspend fun signOut() {
        try {
            val basePath = serverConfigRepository.currentBaseUrl()?.let { "$it/api/mobile/v1" }
            if (basePath != null) {
                authApiFactory(basePath).mobileLogout()
            }
        } catch (e: Exception) {
            // Best effort by construction — see the KDoc above. No failure
            // (network, 5xx, a 401 from an already dead token, an unexpected
            // response) may stop the device from signing out.
        } finally {
            session.signOut()
            // The read cache holds this user's RESPONSES — sessions, audit
            // trail, secrets, conversations. Leaving it on disk after sign out
            // would make them readable, offline and with no token at all, by
            // whoever got into this device next. It sits in the `finally` for
            // the same reason as `session.signOut()`: signing out cannot
            // depend on the server-side revocation having worked.
            CacheDeLeitura.limpar()
        }
    }
}
