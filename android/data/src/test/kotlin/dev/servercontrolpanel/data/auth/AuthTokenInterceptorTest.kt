package dev.servercontrolpanel.data.auth

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
 * Against a real HTTP server: the token reaches the request and a 401 leads to a renewal and
 * replay. The generated `ApiClient` adds the Bearer header but never renews, so this is the
 * interceptor's job.
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
        // Leaves the global ApiClient token alone; the mirror is covered in SessionManagerTest.
        publishAccessToken = {},
    )

    private fun clientFor(session: SessionManager): OkHttpClient =
        OkHttpClient.Builder().addInterceptor(AuthTokenInterceptor(session)).build()

    private fun get(client: OkHttpClient, path: String) =
        client.newCall(Request.Builder().url(server.url(path)).build()).execute()

    @Test
    fun `every authenticated request carries a Bearer token`() {
        server.enqueue(MockResponse().setResponseCode(200).setBody("{}"))
        val session = sessionWith(StubRefresher(RefreshOutcome.Unavailable))

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(200, it.code) }

        assertEquals("Bearer velho", server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun `a 401 renews the session and replays the request with the new token`() {
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
    fun `a rejected renewal returns the 401 and ends the session, leading to login`() {
        // SignedOut is what MainActivity observes to show the login screen.
        server.enqueue(MockResponse().setResponseCode(401))
        val refresher = StubRefresher(RefreshOutcome.Rejected)
        val session = sessionWith(refresher)

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(401, it.code) }

        assertEquals(1, refresher.calls.get())
        assertEquals("one renewal attempt, never a loop", 1, server.requestCount)
        assertEquals(SessionState.SignedOut, session.state.value)
    }

    @Test
    fun `an unavailable renewal returns the 401 without ending the session`() {
        server.enqueue(MockResponse().setResponseCode(401))
        val session = sessionWith(StubRefresher(RefreshOutcome.Unavailable))

        get(clientFor(session), "/api/mobile/v1/me").use { assertEquals(401, it.code) }

        assertTrue(session.state.value is SessionState.SignedIn)
    }

    @Test
    fun `concurrent calls getting 401 trigger a single renewal`() {
        // Uses real OkHttp dispatcher threads, not the runTest virtual scheduler.
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
            Thread { get(client, "/api/mobile/v1/me").use { response -> check(response.code == 200) } }
        }
        threads.forEach { it.start() }
        threads.forEach { it.join() }

        assertEquals(
            "N concurrent 401 responses must not cause N refreshes, the " +
                "BFF rotates refresh tokens and a second renewal would kill the session",
            1,
            refresher.calls.get(),
        )
    }

    @Test
    fun `the refresh route skips 401 handling to avoid recursion`() {
        // Otherwise a dead refresh token would trigger renewals of the renewal forever.
        server.enqueue(MockResponse().setResponseCode(401))
        val refresher = StubRefresher(RefreshOutcome.Renewed("novo", "r2", 900))
        val session = sessionWith(refresher)

        get(clientFor(session), "/api/mobile/v1/auth/refresh").use { assertEquals(401, it.code) }

        assertEquals(0, refresher.calls.get())
        assertEquals(1, server.requestCount)
        assertNull("a public route carries no Bearer", server.takeRequest().getHeader("Authorization"))
    }

    @Test
    fun `the other public auth routes also go without Bearer`() {
        listOf(
            "/api/mobile/v1/auth/login",
            "/api/mobile/v1/auth/pair",
            "/api/mobile/v1/auth/passkey/login/begin",
            "/api/mobile/v1/auth/passkey/login/finish",
            "/api/mobile/v1/auth/passkey/register/begin",
            "/api/mobile/v1/auth/passkey/register/finish",
        ).forEach { path ->
            assertTrue("$path should be public", isPublicAuthPath(path))
        }
        // /auth/logout is protected: it needs the Bearer to revoke its own jti.
        assertTrue(!isPublicAuthPath("/api/mobile/v1/auth/logout"))
        assertTrue(!isPublicAuthPath("/api/mobile/v1/me"))
    }
}
