package com.vpsmanager.data.media

import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import okhttp3.Call
import okhttp3.Interceptor
import okhttp3.Response

/**
 * Attaches [ApiClient.accessToken] as `Authorization: Bearer` to every
 * request that goes through [mediaCallFactory] -- the same companion-level
 * value every generated `ApiClient`'s own `accessTokenProvider` hook reads,
 * and the same header [com.vpsmanager.data.whatsapp.OkHttpWhatsAppWsFactory]
 * attaches by hand for the `/ws/whatsapp` socket.
 *
 * [com.vpsmanager.data.auth.SessionManager] writes that value on every
 * session change (login, renewal, logout), so the header here is real --
 * before a login flow existed it was always absent and EVERY authenticated
 * request came back 401. It is read on every request (an interceptor, not a
 * value captured when the client was built), so a renewal in the middle of a
 * long session applies to media bytes with no cache at all to invalidate.
 *
 * Renewal on a 401 does NOT happen here: that is handled by the
 * [com.vpsmanager.data.auth.AuthTokenInterceptor] installed on the
 * `ApiClient.builder`, and this client derives from [ApiClient.defaultClient]
 * via `newBuilder()`, inheriting that interceptor. This local interceptor
 * stays as a guarantee that the header is there even if the installation
 * order changes; the two write exactly the same value.
 */
private object MediaAuthInterceptor : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val token = ApiClient.accessToken
        val request = if (token != null) {
            chain.request().newBuilder().header("Authorization", "Bearer $token").build()
        } else {
            chain.request()
        }
        return chain.proceed(request)
    }
}

/**
 * The shared authenticated [Call.Factory] for media bytes (thumbnails,
 * streamed video/audio, document downloads) -- everything Coil's
 * `ImageLoader` and Media3's `OkHttpDataSource` issue their Range/GET
 * requests through. Derived from [ApiClient.defaultClient] via
 * `newBuilder()`, which keeps the same connection pool, dispatcher and
 * TLS/proxy configuration as every other BFF call already trusted app-wide --
 * this is deliberately NOT a second, independently-configured `OkHttpClient`;
 * it is the one shared client plus one additional interceptor. Only
 * [createMediaImageLoader] (Coil) and [createMediaDataSourceFactory] (Media3)
 * call this -- never referenced outside `:data`: this module is the only one
 * allowed to construct an `okhttp3.Call.Factory`.
 */
fun mediaCallFactory(): Call.Factory =
    ApiClient.defaultClient.newBuilder().addInterceptor(MediaAuthInterceptor).build()
