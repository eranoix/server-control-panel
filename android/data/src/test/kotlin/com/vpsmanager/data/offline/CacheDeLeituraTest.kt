package com.vpsmanager.data.offline

import java.io.File
import java.nio.file.Files
import okhttp3.Cache
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * What these tests prove, and why it matters: without them, "the app works
 * offline" would be a claim about library configuration, and HTTP cache
 * configuration fails silently — one missing header and the cache exists,
 * takes up disk and never serves anything.
 *
 * That is why the server is really BROUGHT DOWN in the middle of the test,
 * instead of a stand-in that pretends to fail: the path exercised is the same
 * `IOException` a device with no network produces.
 */
class CacheDeLeituraTest {

    private lateinit var servidor: MockWebServer
    private lateinit var dir: File
    private lateinit var client: OkHttpClient

    @Before
    fun montar() {
        servidor = MockWebServer()
        servidor.start()
        dir = Files.createTempDirectory("cache-de-leitura").toFile()
        client = OkHttpClient.Builder()
            .cache(Cache(dir, 4L * 1024 * 1024))
            .addNetworkInterceptor(CacheDeLeitura.TornaCacheavel)
            .addInterceptor(CacheDeLeitura.ServeDoCacheQuandoAFalta)
            .build()
    }

    @After
    fun desmontar() {
        runCatching { servidor.shutdown() }
        dir.deleteRecursively()
    }

    private fun get(caminho: String) =
        client.newCall(Request.Builder().url(servidor.url(caminho)).build()).execute()

    @Test
    fun `com o servidor fora do ar a resposta anterior continua sendo servida`() {
        servidor.enqueue(MockResponse().setBody("""{"cpu":21}"""))
        get("/ops/status").use { assertEquals("""{"cpu":21}""", it.body?.string()) }

        // The internet really drops — not a stand-in pretending.
        servidor.shutdown()

        get("/ops/status").use { resposta ->
            assertEquals(200, resposta.code)
            assertEquals("""{"cpu":21}""", resposta.body?.string())
            assertTrue("a resposta tem de vir do cache", resposta.networkResponse == null)
        }
    }

    /**
     * A route never seen cannot pretend it loaded: OkHttp returns a 504
     * "Unsatisfiable Request", which the caller maps to the usual error.
     */
    @Test
    fun `rota nunca vista sem rede devolve 504, nao um corpo inventado`() {
        servidor.shutdown()

        get("/nunca-visitada").use { resposta ->
            assertEquals(504, resposta.code)
        }
    }

    /**
     * The server's `no-store` wins. If the BFF marks a route as not storable,
     * it has a reason the network layer does not know about.
     */
    @Test
    fun `no-store do servidor e respeitado e nada e guardado`() {
        servidor.enqueue(
            MockResponse().setBody("segredo").addHeader("Cache-Control", "no-store"),
        )
        get("/security/secrets").use { it.body?.string() }

        servidor.shutdown()

        get("/security/secrets").use { resposta ->
            assertEquals("nada marcado como no-store pode sobreviver a queda", 504, resposta.code)
        }
    }

    /**
     * WITH network, always revalidate: on a screen whose purpose is to say what
     * is happening right now, serving 30 seconds of cache is showing a wrong number.
     */
    @Test
    fun `com rede a resposta e sempre a nova, nunca a guardada`() {
        servidor.enqueue(MockResponse().setBody("""{"cpu":21}"""))
        get("/ops/status").use { it.body?.string() }

        servidor.enqueue(MockResponse().setBody("""{"cpu":88}"""))
        get("/ops/status").use { resposta ->
            assertEquals("""{"cpu":88}""", resposta.body?.string())
        }
    }
}
