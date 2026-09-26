package com.vpsmanager.data.offline

import android.content.Context
import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import java.io.File
import java.util.concurrent.TimeUnit
import okhttp3.Cache
import okhttp3.CacheControl
import okhttp3.Interceptor
import okhttp3.Response

/**
 * Keeps the app usable without internet by serving stored `GET` responses.
 *
 * One `OkHttpClient` feeds the generated client, WhatsApp media and SDUI, so
 * installing the cache there covers every screen without feature modules knowing
 * about offline mode. An HTTP cache rather than a local database: it covers all
 * routes at once and becomes harmless if a repository later reads from Room.
 *
 * 1. A disk [Cache] in the app's private directory.
 * 2. A network interceptor that makes responses cacheable (the BFF sends no
 *    `Cache-Control`, and without it OkHttp stores nothing).
 * 3. An application interceptor that replays the request against the cache when
 *    the network fails.
 *
 * Only `GET` is cached: serving a stale write response would claim something
 * happened. Offline writes go through [Outbox].
 */
object ReadCache {

    /**
     * 24 MiB. BFF responses are small JSON (the largest is under 100 KB), and
     * WhatsApp media has its own bounded cache.
     */
    private const val CAP_BYTES = 24L * 1024 * 1024

    /**
     * How long a stored response is served when there is NO network: seven days.
     * Old server state is still useful as long as the screen shows its age.
     */
    private val MAX_STALE_OFFLINE = TimeUnit.DAYS.toSeconds(7).toInt()

    /**
     * How long a response is served without the network while online: zero,
     * always revalidate, since the screens exist to show current state.
     */
    private const val MAX_AGE_ONLINE = 0

    @Volatile
    private var installed = false

    /**
     * Installs the cache and policy on the shared `OkHttpClient`.
     *
     * Must run in `Application.onCreate` before any `*Api` touches the client:
     * `ApiClient.defaultClient` is `by lazy { builder.build() }`, so later changes
     * to the builder are silently ignored.
     */
    fun install(context: Context) {
        if (installed) return
        installed = true

        val dir = File(context.applicationContext.cacheDir, "bff-http")
        ApiClient.builder
            .cache(Cache(dir, CAP_BYTES))
            .addNetworkInterceptor(MakeCacheable)
            .addInterceptor(ServeFromCacheWhenOffline)
    }

    /**
     * Erases everything stored. Called on sign-out, since cached responses hold
     * that user's data and would otherwise stay readable offline without a token.
     */
    fun clear() {
        runCatching { (ApiClient.defaultClient.cache)?.evictAll() }
    }

    /**
     * Routes whose body is a slice of a live stream and must never be stored.
     *
     * The app replays the terminal log into libghostty-vt to rebuild the session;
     * an old slice applied before the live stream corrupts the screen (relative
     * cursor moves paint over text), and the disk cache survives app restarts.
     * The server already sends `no-store` for these, which [MakeCacheable]
     * respects; this list covers older servers and routes someone forgets to mark.
     */
    private val VOLATILE_ROUTES = listOf(
        "/terminal/log-bruto",
        "/terminal/historico",
        "/terminal/scrollback",
    )

    internal fun isVolatile(path: String): Boolean =
        VOLATILE_ROUTES.any { path.endsWith(it) }

    /**
     * Rewrites the response `Cache-Control` so OkHttp agrees to store it. It must
     * be a network interceptor, which sees the response before the cache decides.
     * The server's `no-store` is respected.
     */
    internal object MakeCacheable : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val response = chain.proceed(chain.request())
            val request = chain.request()
            // As a network interceptor, reaching here means the response came from
            // the server, never the cache, so this is where DataAge's stamp comes from.
            DataAge.recordNetworkResponse()
            val storable = request.method == "GET" &&
                response.isSuccessful &&
                !isVolatile(request.url.encodedPath) &&
                !response.header("Cache-Control").orEmpty().contains("no-store")
            if (!storable) return response
            return response.newBuilder()
                .removeHeader("Pragma") // HTTP/1.0 header that would override the rest
                .header("Cache-Control", "public, max-age=$MAX_AGE_ONLINE")
                .build()
        }
    }

    /**
     * When the network fails, replays the SAME request against the cache.
     *
     * It always tries the network first: checking connectivity beforehand races
     * with reality, and Android 15+ cuts background network without changing
     * capabilities. Only `IOException` falls back; a 500 is a real response and
     * hiding it would hide a broken server.
     */
    internal object ServeFromCacheWhenOffline : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val request = chain.request()
            if (request.method != "GET") return chain.proceed(request)
            // A volatile route has no acceptable stale response; fail and let the
            // caller rely on the live stream.
            if (isVolatile(request.url.encodedPath)) return chain.proceed(request)

            return try {
                chain.proceed(request)
            } catch (e: java.io.IOException) {
                val fromCache = request.newBuilder()
                    .cacheControl(
                        CacheControl.Builder()
                            .onlyIfCached()
                            .maxStale(MAX_STALE_OFFLINE, TimeUnit.SECONDS)
                            .build(),
                    )
                    .build()
                // With nothing stored, OkHttp returns a 504, which the caller maps
                // to the usual "check your connection" error.
                chain.proceed(fromCache)
            }
        }
    }
}
