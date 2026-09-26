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
 * The app stops dying without internet.
 *
 * ## The problem, and why it belonged to EVERY screen at once
 *
 * Every screen in this app does a `GET` against the BFF and maps the failure to
 * an error state. With no network, all 72 routes raise `UnknownHostException`
 * at the same time, and the whole app becomes a collection of cards saying
 * "check your connection" — including for data that does not change minute to
 * minute and that the device has just received.
 *
 * Fixing that screen by screen would be fixing the same defect 40 times. There
 * is a single point: **one `OkHttpClient` feeds the generated client, WhatsApp
 * media and SDUI**. Installing the cache and the policy there covers
 * everything, and no feature module has to know offline mode exists.
 *
 * ## Why an HTTP cache, and not a local database (yet)
 *
 * The pattern the industry adopts for true *offline-first* is local data as the
 * source of truth (Room) with the network becoming just one input — it is what
 * Android's official guide and Now in Android do. It is better, and it is
 * considerably more expensive: a schema per screen, migrations, and a conflict
 * decision for every write.
 *
 * The HTTP cache delivers TODAY the half the owner asked for first — "don't cut
 * off my access to the app if the internet drops" — for all 72 routes at once,
 * and it does not get in the way of the migration later: a repository that
 * starts reading from Room simply stops consulting the network, and this cache
 * becomes harmless.
 *
 * ## The three pieces
 *
 * 1. **A disk [Cache]** in the app's private directory.
 * 2. **A network interceptor** that makes the response cacheable. Necessary
 *    because the BFF sends no `Cache-Control`, and without that header OkHttp
 *    **stores nothing** — the cache would exist and stay permanently empty.
 * 3. **An application interceptor** that, when the network fails, replays the
 *    same request against the cache instead of propagating the exception.
 *
 * ## What is NEVER served from cache
 *
 * Only `GET` enters. A write has no cacheable response, and serving a stale
 * `POST` response would be inventing that something happened. Offline writing
 * is the other side of the problem and needs a queue with resend — not a cache.
 */
object CacheDeLeitura {

    /**
     * 24 MiB. The BFF's responses are small JSON (the largest, the screen
     * catalogue, does not reach 100 KB); what takes up space is WhatsApp media,
     * which already has its own bounded cache. This ceiling covers weeks of
     * normal browsing and is a fraction of what a 54 MB APK already occupies.
     */
    private const val TETO_BYTES = 24L * 1024 * 1024

    /**
     * How long a stored response keeps being served when there is NO network.
     *
     * Seven days, and the number is chosen from what the data means: this app
     * shows server state. Yesterday's data, LABELLED as yesterday's, is useful
     * — it says what was running, which containers existed, what the disk
     * looked like. What must not happen is it passing itself off as current,
     * and that is solved by the screen stating the time, not by the cache
     * shortening the window.
     */
    private val VALIDADE_SEM_REDE = TimeUnit.DAYS.toSeconds(7).toInt()

    /**
     * How long a response is served WITHOUT going to the network, even online.
     *
     * Zero: with a network, always revalidate. The latency gain of a short
     * cache does not make up for showing a wrong container count for 30 seconds
     * on a screen whose entire purpose is to say what is happening NOW.
     */
    private const val VALIDADE_COM_REDE = 0

    @Volatile
    private var instalado = false

    /**
     * Installs the cache and the policy on the shared `OkHttpClient`.
     *
     * ORDER IS CRITICAL, the same as `SessionNetworking.install`:
     * `ApiClient.defaultClient` is a `by lazy { builder.build() }`, so this has
     * to run in `Application.onCreate`, before any `*Api` touches the client.
     * After that, touching the builder has no effect whatsoever — and the
     * symptom would be an app that simply carries on dying offline, with no
     * error pointing here.
     */
    fun instalar(context: Context) {
        if (instalado) return
        instalado = true

        val dir = File(context.applicationContext.cacheDir, "bff-http")
        ApiClient.builder
            .cache(Cache(dir, TETO_BYTES))
            .addNetworkInterceptor(TornaCacheavel)
            .addInterceptor(ServeDoCacheQuandoAFalta)
    }

    /**
     * Erases everything that was stored.
     *
     * Called on sign-out: the cached responses are THAT user's data — sessions,
     * audit trail, secrets, conversations. Leaving them on disk after logout
     * would make them readable by the next person to pick up this device,
     * offline, with no token at all.
     */
    fun limpar() {
        runCatching { (ApiClient.defaultClient.cache)?.evictAll() }
    }

    /**
     * Routes whose body is a SLICE OF A LIVE STREAM, and which therefore can
     * NEVER be stored.
     *
     * ## The defect this list closes
     *
     * `TornaCacheavel` made every successful GET cacheable, and
     * `ServeDoCacheQuandoAFalta` serves a copy up to seven days old when the
     * network fails — which Android 15+ brings about by itself when it cuts the
     * app's network in the background, precisely at the moment the terminal
     * screen opens.
     *
     * For a container dashboard that is the desired behaviour: yesterday's
     * data, labelled, is useful. For the terminal's log it is something else.
     * The app REPLAYS that log into libghostty-vt itself to rebuild the
     * session; an old slice applied before the live stream does not make the
     * screen stale, it **corrupts** it — the frame uses relative cursor
     * movement and column jumps that do not erase what they skip over, so an
     * old frame applied from a different position paints rules over text.
     *
     * And because the cache lives on disk, it survived restarting the app. The
     * owner only escaped by wiping the entire storage — and it was that gesture
     * of his that pointed here.
     *
     * ## Why the list exists if the server already sends `no-store`
     *
     * The server does send it (see `semCache`, in `internal/mobilebff`), and
     * [TornaCacheavel] respects it. This list is the belt: a device pointed at
     * an older server, or a new route someone forgets to mark, must not cost a
     * corrupted screen. The criterion is objective and fits in one sentence:
     * **if the same URL returns different content every second, it has no "the
     * response" to store.**
     */
    private val ROTAS_VOLATEIS = listOf(
        "/terminal/log-bruto",
        "/terminal/historico",
        "/terminal/scrollback",
    )

    internal fun ehVolatil(caminho: String): Boolean =
        ROTAS_VOLATEIS.any { caminho.endsWith(it) }

    /**
     * Rewrites the RESPONSE's `Cache-Control` so that OkHttp agrees to store it.
     *
     * It has to be a **network interceptor**: it sees the response as it
     * arrives from the server, before the cache decides what to do with it. As
     * an application interceptor it would be too late — the decision to store
     * would already have been taken (and it would have been "no").
     *
     * The server's `no-store` is respected. If the BFF marks a route as
     * non-storable, it has a reason this file does not know about.
     */
    internal object TornaCacheavel : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val resposta = chain.proceed(chain.request())
            val pedido = chain.request()
            // A NETWORK interceptor: reaching here means the response came
            // from the server, never from the cache. It is the only point in
            // the app that knows that for certain, and so it is here that the
            // stamp the offline banner shows comes from. See IdadeDoDado.
            IdadeDoDado.registrarRespostaDaRede()
            val armazenavel = pedido.method == "GET" &&
                resposta.isSuccessful &&
                !ehVolatil(pedido.url.encodedPath) &&
                !resposta.header("Cache-Control").orEmpty().contains("no-store")
            if (!armazenavel) return resposta
            return resposta.newBuilder()
                .removeHeader("Pragma") // HTTP/1.0; sobrevive e cancela o resto
                .header("Cache-Control", "public, max-age=$VALIDADE_COM_REDE")
                .build()
        }
    }

    /**
     * When the network fails, replays the SAME request against the cache.
     *
     * ## Why it always tries first
     *
     * This interceptor does not ask "am I online?" before setting out. Asking
     * first is a race already lost — the answer goes stale between the question
     * and the `connect()`, and Android 15+ cuts the app's network in the
     * background without changing any capability. Trying and falling back to
     * the cache gives the right answer in both cases and invents no third one.
     *
     * ## Why only `IOException`
     *
     * A network failure is an `IOException`. A 500 from the server is NOT:
     * there was a response, and swapping it for a stale copy would hide that
     * the server is broken — which is information the owner needs to see.
     */
    internal object ServeDoCacheQuandoAFalta : Interceptor {
        override fun intercept(chain: Interceptor.Chain): Response {
            val pedido = chain.request()
            if (pedido.method != "GET") return chain.proceed(pedido)
            // A volatile route with no network has no acceptable stale
            // response: better to fail and let the caller carry on with the
            // live stream alone than to rebuild the screen from a slice days
            // old.
            if (ehVolatil(pedido.url.encodedPath)) return chain.proceed(pedido)

            return try {
                chain.proceed(pedido)
            } catch (e: java.io.IOException) {
                val doCache = pedido.newBuilder()
                    .cacheControl(
                        CacheControl.Builder()
                            .onlyIfCached()
                            .maxStale(VALIDADE_SEM_REDE, TimeUnit.SECONDS)
                            .build(),
                    )
                    .build()
                // With nothing stored, OkHttp returns a 504 "Unsatisfiable
                // Request" — which is an HTTP response, not an exception. The
                // caller maps 504 to the same error as always, so a screen
                // never seen before still says "check your connection" instead
                // of pretending it loaded.
                chain.proceed(doCache)
            }
        }
    }
}
