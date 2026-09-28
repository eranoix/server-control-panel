package dev.servercontrolpanel.data.auth

import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.Request
import okhttp3.Response

private const val HTTP_UNAUTHORIZED = 401

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

class AuthTokenInterceptor(private val session: SessionManager) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (isPublicAuthPath(request.url.encodedPath)) return chain.proceed(request)

        val token = runBlocking { session.accessTokenForRequest() }
        val response = chain.proceed(request.withBearer(token))
        if (response.code != HTTP_UNAUTHORIZED) return response

        val renewed = runBlocking { session.refreshAfterUnauthorized(token) }
        if (renewed == null || renewed == token) return response

        response.close()
        return chain.proceed(request.withBearer(renewed))
    }
}

private fun Request.withBearer(token: String?): Request =
    if (token == null) this else newBuilder().header("Authorization", "Bearer $token").build()

object SessionNetworking {

    @Volatile
    private var installed = false

    fun install(session: SessionManager) {
        if (installed) return
        installed = true
        ApiClient.builder.addInterceptor(AuthTokenInterceptor(session))
    }
}
