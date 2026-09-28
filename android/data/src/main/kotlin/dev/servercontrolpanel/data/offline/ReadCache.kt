package dev.servercontrolpanel.data.offline

import android.content.Context
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import java.io.File
import java.util.concurrent.TimeUnit
import okhttp3.Cache
import okhttp3.CacheControl
import okhttp3.Interceptor
import okhttp3.Response

object ReadCache {

    private const val CAP_BYTES = 24L * 1024 * 1024

    private val MAX_STALE_OFFLINE = TimeUnit.DAYS.toSeconds(7).toInt()

    private const val MAX_AGE_ONLINE = 0

    @Volatile
    private var installed = false

    fun install(context: Context) {
        if (installed) return
        installed = true

        val dir = File(context.applicationContext.cacheDir, "bff-http")
        ApiClient.builder
            .cache(Cache(dir, CAP_BYTES))
            .addNetworkInterceptor(MakeCacheable)
            .addInterceptor(ServeFromCacheWhenOffline)
    }

    fun clear() {
        runCatching { (ApiClient.defaultClient.cache)?.evictAll() }
    }

    private val VOLATILE_ROUTES = listOf(
        "/terminal/raw-log",
        "/terminal/history",
        "/terminal/scrollback",
    )

    internal fun isVolatile(path: String): Boolean =
        VOLATILE_ROUTES.any { path.endsWith(it) }

    internal object MakeCacheable : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val response = chain.proceed(chain.request())
            val request = chain.request()
            DataAge.recordNetworkResponse()
            val storable = request.method == "GET" &&
                response.isSuccessful &&
                !isVolatile(request.url.encodedPath) &&
                !response.header("Cache-Control").orEmpty().contains("no-store")
            if (!storable) return response
            return response.newBuilder()
                .removeHeader("Pragma")
                .header("Cache-Control", "public, max-age=$MAX_AGE_ONLINE")
                .build()
        }
    }

    internal object ServeFromCacheWhenOffline : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val request = chain.request()
            if (request.method != "GET") return chain.proceed(request)
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
                chain.proceed(fromCache)
            }
        }
    }
}
