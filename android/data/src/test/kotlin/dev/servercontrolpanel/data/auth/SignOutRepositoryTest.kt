package dev.servercontrolpanel.data.auth

import dev.servercontrolpanel.core.model.ServerConfig
import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.data.config.ServerConfigStore
import dev.servercontrolpanel.mobileapiclient.api.AuthApi
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.IOException

/**
 * [SignOutRepository]: server-side revocation happens on the right path, the local session always
 * ends (even when the server never answers), and with no server configured signing out still works.
 */
class SignOutRepositoryTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private class FakeStore(private var config: ServerConfig?) : ServerConfigStore {
        override fun load(): ServerConfig? = config
        override fun save(config: ServerConfig) {
            this.config = config
        }

        override fun clear() {
            config = null
        }
    }

    private class NeverCalledRefresher : SessionRefresher {
        override suspend fun refresh(refreshToken: String): RefreshOutcome =
            throw AssertionError("signing out must not renew the session")
    }

    private fun signedInSession(): SessionManager = SessionManager(
        tokenStore = InMemoryTokenStore(),
        refresher = NeverCalledRefresher(),
        // Keeps the global `ApiClient` token from leaking between cases.
        publishAccessToken = {},
    ).apply { establish(accessToken = "access-1", refreshToken = "refresh-1", expiresInSeconds = 900) }

    private fun repository(session: SessionManager, baseUrl: String?): SignOutRepository = SignOutRepository(
        session = session,
        serverConfigRepository = ServerConfigRepository(
            FakeStore(baseUrl?.let { ServerConfig(baseUrl = it) }),
        ),
        authApiFactory = { basePath -> AuthApi(basePath) },
    )

    @Test
    fun `revokes on the BFF and ends the local session`() = runTest {
        server.enqueue(MockResponse().setResponseCode(200).setBody("""{"ok":true}"""))
        val session = signedInSession()
        assertTrue(session.state.value is SessionState.SignedIn)

        repository(session, server.url("/").toString().trimEnd('/')).signOut()

        val request = server.takeRequest()
        assertEquals("POST", request.method)
        assertEquals("/api/mobile/v1/auth/logout", request.path)
        assertTrue(session.state.value is SessionState.SignedOut)
        assertNull(session.currentAccessToken())
    }

    /** Signing out must work offline, otherwise the session would stay alive on the device. */
    @Test
    fun `a server that is down does not block local sign out`() = runTest {
        server.shutdown()
        val session = signedInSession()

        repository(session, server.url("/").toString().trimEnd('/')).signOut()

        assertTrue(session.state.value is SessionState.SignedOut)
        assertNull(session.currentAccessToken())
    }

    @Test
    fun `a server error response does not block local sign out`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))
        val session = signedInSession()

        repository(session, server.url("/").toString().trimEnd('/')).signOut()

        assertTrue(session.state.value is SessionState.SignedOut)
        assertNull(session.currentAccessToken())
    }

    @Test
    fun `without a configured server it signs out without any network call`() = runTest {
        val session = signedInSession()
        var apiBuilt = false

        SignOutRepository(
            session = session,
            serverConfigRepository = ServerConfigRepository(FakeStore(null)),
            authApiFactory = {
                apiBuilt = true
                throw IOException("no call expected")
            },
        ).signOut()

        assertTrue(session.state.value is SessionState.SignedOut)
        assertTrue("no AuthApi should be built", !apiBuilt)
    }
}
