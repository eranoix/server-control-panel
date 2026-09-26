package com.vpsmanager.data.auth

import com.vpsmanager.core.model.ServerConfig
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.data.config.ServerConfigStore
import com.vpsmanager.mobileapiclient.api.AuthApi
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
 * [SignOutRepository] — the missing half that gives the app a way to sign out.
 *
 * The three things these tests pin, in the order they matter:
 *  1. the server-side revocation really happens, on the right path;
 *  2. the local session ALWAYS drops, including when the server never answers;
 *  3. with no server configured, signing out still signs out (not an error).
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
            throw AssertionError("sair não pode renovar a sessão")
    }

    private fun signedInSession(): SessionManager = SessionManager(
        tokenStore = InMemoryTokenStore(),
        refresher = NeverCalledRefresher(),
        // The real network interceptor takes no part in this test: `publishAccessToken`
        // is swapped out so `ApiClient`'s global companion does not leak between cases.
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
    fun `revoga no BFF e derruba a sessao local`() = runTest {
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

    /**
     * The case that decides whether this button is any good: signing out on a
     * plane. A "Sign out" that only signs out when the network is good would
     * leave the session alive on the device — the opposite of what the
     * operator asked for by tapping it.
     */
    @Test
    fun `servidor fora do ar nao impede o logout local`() = runTest {
        server.shutdown()
        val session = signedInSession()

        repository(session, server.url("/").toString().trimEnd('/')).signOut()

        assertTrue(session.state.value is SessionState.SignedOut)
        assertNull(session.currentAccessToken())
    }

    @Test
    fun `resposta de erro do servidor nao impede o logout local`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))
        val session = signedInSession()

        repository(session, server.url("/").toString().trimEnd('/')).signOut()

        assertTrue(session.state.value is SessionState.SignedOut)
        assertNull(session.currentAccessToken())
    }

    @Test
    fun `sem servidor configurado sai sem chamar rede nenhuma`() = runTest {
        val session = signedInSession()
        var apiBuilt = false

        SignOutRepository(
            session = session,
            serverConfigRepository = ServerConfigRepository(FakeStore(null)),
            authApiFactory = {
                apiBuilt = true
                throw IOException("não deveria haver chamada")
            },
        ).signOut()

        assertTrue(session.state.value is SessionState.SignedOut)
        assertTrue("nenhum AuthApi deveria ser construído", !apiBuilt)
    }
}
