package com.vpsmanager.data.auth

import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.Request
import okhttp3.Response

private const val HTTP_UNAUTHORIZED = 401

/**
 * The mobile BFF routes that sit OUTSIDE `auth.Middleware` — the list comes
 * from `RegisterPublic` in `internal/mobilebff` (`auth_login.go`,
 * `auth_pairing.go`, `auth_passkey.go`). Compared by suffix because the real
 * path carries the `/api/mobile/v1` prefix.
 *
 * The entry that CANNOT be missing here is `auth/refresh`: if the renewal call
 * itself went through the 401 handling below, a dead refresh token would fire
 * a renewal to fix the renewal, forever. The others are on the list for
 * correctness (they have no session to send), not because of any recursion
 * risk. `auth/logout` is deliberately off the list: it is protected and needs
 * the Bearer in order to revoke its own jti.
 */
private val PUBLIC_PATH_SUFFIXES = listOf(
    "/auth/login",
    "/auth/refresh",
    "/auth/pair",
    "/auth/passkey/register/begin",
    "/auth/passkey/register/finish",
    "/auth/passkey/login/begin",
    "/auth/passkey/login/finish",
)

internal fun isPublicAuthPath(encodedPath: String): Boolean =
    PUBLIC_PATH_SUFFIXES.any { encodedPath.endsWith(it) }

/**
 * Attaches `Authorization: Bearer <access token>` to every BFF call and, on a
 * 401, renews the session ONCE and replays the request.
 *
 * Why an interceptor and not just the generated client's
 * `accessTokenProvider`: ever since the BFF OpenAPI spec started declaring
 * `securitySchemes` (bearerAuth), `openapi-generator` emits an `ApiClient`
 * with `updateAuthParams()`, which builds `Authorization: Bearer` on its own
 * for every operation marked `requiresAuthentication` — the header itself, in
 * other words, the generated code already knows how to build. What it does NOT
 * know how to do, and never will because it is 100% generated, is react to a
 * 401: RENEW the session and REPLAY the request. That is the only reason this
 * class exists.
 *
 * Nor is the header duplicated with the generated one: `Request.withBearer` (below) uses
 * `header()`, which replaces the value instead of appending, so the request
 * goes out with a single `Authorization` — this class's, and on the second
 * attempt already carrying the renewed token. That is why the patch sits on
 * the outside, in the shared `OkHttpClient` every `*Api` uses by default.
 *
 * The retry is a single attempt by construction: [SessionManager.refreshAfterUnauthorized]
 * returns `null` when there is no way to renew, and in that case the ORIGINAL
 * 401 is handed back untouched to the repository layer. No loop.
 */
class AuthTokenInterceptor(private val session: SessionManager) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (isPublicAuthPath(request.url.encodedPath)) return chain.proceed(request)

        // runBlocking inside an interceptor is safe here: OkHttp interceptors
        // already run on a dispatcher I/O thread, never on main. What it blocks
        // is the call itself — which is waiting for a response anyway.
        val token = runBlocking { session.accessTokenForRequest() }
        val response = chain.proceed(request.withBearer(token))
        if (response.code != HTTP_UNAUTHORIZED) return response

        val renewed = runBlocking { session.refreshAfterUnauthorized(token) }
        // `renewed == token` should not happen (the server always issues a new
        // access token), but replaying the request with the SAME token that
        // just took a 401 would only produce a second 401 — return the first
        // one and let the UI lead to the login screen.
        if (renewed == null || renewed == token) return response

        response.close()
        return chain.proceed(request.withBearer(renewed))
    }
}

private fun Request.withBearer(token: String?): Request =
    if (token == null) this else newBuilder().header("Authorization", "Bearer $token").build()

/**
 * Installs [AuthTokenInterceptor] in the generated client's shared
 * `OkHttpClient`.
 *
 * ORDER MATTERS: `ApiClient.defaultClient` is a `by lazy { builder.build() }`,
 * so adding the interceptor to the `builder` only has an effect BEFORE the
 * client is built for the first time. That is why the right place to call it
 * is `Application.onCreate` — before any `*Api` exists. Idempotent
 * ([installed]) so that a second `onCreate` (or a test) does not stack two
 * identical interceptors.
 */
object SessionNetworking {

    @Volatile
    private var installed = false

    fun install(session: SessionManager) {
        if (installed) return
        installed = true
        ApiClient.builder.addInterceptor(AuthTokenInterceptor(session))
    }
}
