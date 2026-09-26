package com.vpsmanager.data.auth

import java.util.concurrent.atomic.AtomicInteger
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Proves, against a real HTTP server, that the token REACHES the request and
 * that a 401 leads to a renewal — and not to a wall.
 *
 * The renewal is the part that only exists here. The generated `ApiClient` now
 * builds `Authorization: Bearer` by itself (the BFF spec declares
 * `securitySchemes`), but it reads the token ONCE and hands the 401 back as it
 * is — it neither renews nor replays. That is the behaviour this test proves,
 * and it is what disappears if the interceptor goes.
 */
class AuthTokenInterceptorTest {

    private lateinit var server: MockWebServer

    private var clock = 1_000_000L

    @Before
    fun startServer() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun stopServer() {
        server.shutdown()
    }

    private class StubRefresher(private val outcome: RefreshOutcome) : SessionRefresher {
        val calls = AtomicInteger(0)
        override suspend fun refresh(refreshToken: String): RefreshOutcome {
            calls.incrementAndGet()
            return outcome
        }
    }

    private fun sessionWith(refresher: SessionRefresher, access: String = "velho") = SessionManager(
        tokenStore = InMemoryTokenStore(
            SessionTokens(accessToken = access, refreshToken = "r1", expiresAtEpochMillis = clock + 900_000L),
        ),
        refresher = refresher,
        now = { clock },
        // Does not write to the ApiClient's global companion: this test is about
        // the header that goes out on the wire, not the mirror (covered in SessionManagerTest).
        publishAccessToken = {},
    )

    private fun clientFor(session: SessionManager): OkHttpClient =
        OkHttpClient.Builder().addInterceptor(AuthTokenInterceptor(session)).build()

    private fun get(client: OkHttpClient, path: String) =
        client.newCall(Request.Builder().url(server.url(path)).build()).execute()

    @Test
    fun `toda requisicao autenticada sai com Bearer`() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("{}"))
        val session = sessionWith(StubRefresher(RefreshOutcome.Unavailable))

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(200, it.code) }

        assertEquals("Bearer velho", server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun `um 401 renova a sessao e repete a requisicao com o token novo`() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse =
                if (request.getHeader("Authorization") == "Bearer novo") {
                    MockResponse().setResponseCode(200).setBody("{}")
                } else {
                    MockResponse().setResponseCode(401)
                }
        }
        val refresher = StubRefresher(RefreshOutcome.Renewed("novo", "r2", 900))
        val session = sessionWith(refresher)

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(200, it.code) }

        assertEquals(1, refresher.calls.get())
        assertEquals(2, server.requestCount)
        assertEquals("Bearer velho", server.takeRequest().getHeader("Authorization"))
        assertEquals("Bearer novo", server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun `renovacao recusada devolve o 401 e derruba a sessao — e o caminho que leva ao login`() {
        // Before this, a 401 became a "Try again" card that was never going to
        // work. Now it ends in SignedOut, which is what MainActivity observes
        // in order to show the login screen.
        server.enqueue(MockResponse().setResponseCode(401))
        val refresher = StubRefresher(RefreshOutcome.Rejected)
        val session = sessionWith(refresher)

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(401, it.code) }

        assertEquals(1, refresher.calls.get())
        assertEquals("uma tentativa de renovacao, nunca um laco", 1, server.requestCount)
        assertEquals(SessionState.SignedOut, session.state.value)
    }

    @Test
    fun `renovacao indisponivel devolve o 401 sem derrubar a sessao`() {
        server.enqueue(MockResponse().setResponseCode(401))
        val session = sessionWith(StubRefresher(RefreshOutcome.Unavailable))

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(401, it.code) }

        assertTrue(session.state.value is SessionState.SignedIn)
    }

    @Test
    fun `varias chamadas concorrentes tomando 401 disparam UMA renovacao`() {
        // The end-to-end proof of the single queue, with real threads from
        // OkHttp's dispatcher -- not the virtual scheduler of runTest.
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse =
                if (request.getHeader("Authorization") == "Bearer novo") {
                    MockResponse().setResponseCode(200).setBody("{}")
                } else {
                    MockResponse().setResponseCode(401)
                }
        }
        val refresher = StubRefresher(RefreshOutcome.Renewed("novo", "r2", 900))
        val session = sessionWith(refresher)
        val client = clientFor(session)

        val threads = List(6) {
            Thread { get(client, "/api/mobile/v1/me").use { resposta -> check(resposta.code == 200) } }
        }
        threads.forEach { it.start() }
        threads.forEach { it.join() }

        assertEquals(
            "N chamadas tomando 401 ao mesmo tempo nao podem virar N refreshes — o refresh do " +
                "BFF e rotativo e a segunda renovacao mataria a sessao",
            1,
            refresher.calls.get(),
        )
    }

    @Test
    fun `a rota de renovacao nao passa pelo tratamento de 401 — sem recursao`() {
        // If /auth/refresh entered the retry path, a dead refresh token would
        // fire a renewal to fix the renewal, forever.
        server.enqueue(MockResponse().setResponseCode(401))
        val refresher = StubRefresher(RefreshOutcome.Renewed("novo", "r2", 900))
        val session = sessionWith(refresher)

        get(clientFor(session), "/api/mobile/v1/auth/refresh").use { assertEquals(401, it.code) }

        assertEquals(0, refresher.calls.get())
        assertEquals(1, server.requestCount)
        assertNull("rota publica nao leva Bearer", server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun `as demais rotas publicas de auth tambem saem sem Bearer`() {
        listOf(
            "/api/mobile/v1/auth/login",
            "/api/mobile/v1/auth/pair",
            "/api/mobile/v1/auth/passkey/login/begin",
            "/api/mobile/v1/auth/passkey/login/finish",
            "/api/mobile/v1/auth/passkey/register/begin",
            "/api/mobile/v1/auth/passkey/register/finish",
        ).forEach { path ->
            assertTrue("$path deveria ser publica", isPublicAuthPath(path))
        }
        // /auth/logout is protected: it needs the Bearer to revoke its own jti.
        assertTrue(!isPublicAuthPath("/api/mobile/v1/auth/logout"))
        assertTrue(!isPublicAuthPath("/api/mobile/v1/me"))
    }
}
