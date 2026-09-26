package com.vpsmanager.data.auth

import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pins the session behaviour EVERY screen of the app depends on: storing the
 * token pair, renewing through a single queue, and tearing the session down
 * when there is no longer any way to renew.
 */
class SessionManagerTest {

    private var clock = 1_000_000L

    private fun tokens(access: String = "a1", refresh: String = "r1", ttlSeconds: Long = 900) = SessionTokens(
        accessToken = access,
        refreshToken = refresh,
        expiresAtEpochMillis = clock + ttlSeconds * 1_000L,
    )

    private fun manager(
        store: TokenStore = InMemoryTokenStore(),
        refresher: SessionRefresher,
        published: MutableList<String?> = mutableListOf(),
    ) = SessionManager(
        tokenStore = store,
        refresher = refresher,
        now = { clock },
        publishAccessToken = { published += it },
    )

    @After
    fun clearGlobalToken() {
        ApiClient.accessToken = null
    }

    private class CountingRefresher(
        private val outcome: (Int) -> RefreshOutcome,
        private val gate: CompletableDeferred<Unit>? = null,
    ) : SessionRefresher {
        val calls = AtomicInteger(0)
        override suspend fun refresh(refreshToken: String): RefreshOutcome {
            val n = calls.incrementAndGet()
            gate?.await()
            return outcome(n)
        }
    }

    @Test
    fun `um login estabelece a sessao e publica o access token`() {
        val published = mutableListOf<String?>()
        val store = InMemoryTokenStore()
        val manager = manager(store, CountingRefresher({ RefreshOutcome.Unavailable }), published)

        manager.establish(accessToken = "a1", refreshToken = "r1", expiresInSeconds = 900)

        assertEquals("a1", manager.currentAccessToken())
        assertEquals(SessionState.SignedIn(clock + 900_000L), manager.state.value)
        assertEquals("a1", store.load()?.accessToken)
        assertEquals(listOf(null, "a1"), published)
    }

    @Test
    fun `a sessao gravada e adotada na construcao — o app reabre logado`() {
        val manager = manager(InMemoryTokenStore(tokens()), CountingRefresher({ RefreshOutcome.Unavailable }))

        assertEquals("a1", manager.currentAccessToken())
        assertTrue(manager.state.value is SessionState.SignedIn)
    }

    @Test
    fun `sem guarda persistente o app abre deslogado — o fail-closed do KeystoreTokenStore`() {
        val manager = manager(InMemoryTokenStore(), CountingRefresher({ RefreshOutcome.Unavailable }))

        assertEquals(SessionState.SignedOut, manager.state.value)
        assertNull(manager.currentAccessToken())
    }

    @Test
    fun `varias chamadas tomando 401 ao mesmo tempo disparam UMA unica renovacao`() = runTest {
        // The central point. The BFF refresh is rotating (auth_login.go): the
        // first renewal invalidates the refresh token the others have just
        // sent. N concurrent renewals would not be "waste", they would be the
        // session torn down.
        //
        // The gate holds the first renewal until ALL the concurrent ones have
        // arrived -- without it, the first could finish before the others
        // started and the test would pass even with an implementation that has
        // no queue at all.
        val gate = CompletableDeferred<Unit>()
        val refresher = CountingRefresher(
            outcome = { n -> RefreshOutcome.Renewed("a-novo-$n", "r-novo-$n", 900) },
            gate = gate,
        )
        val manager = manager(InMemoryTokenStore(tokens()), refresher)

        val pedidos = List(8) { async { manager.refreshAfterUnauthorized("a1") } }
        // Runs all 8 until every one of them is blocked: one on the gate
        // (already inside the refresher) and seven on the mutex queue. It is at
        // this point -- all in flight, none finished -- that the count proves
        // the single queue.
        advanceUntilIdle()
        assertEquals("uma renovacao por vencimento, nunca uma por chamada", 1, refresher.calls.get())

        gate.complete(Unit)
        val resultados = pedidos.awaitAll()

        assertEquals("nenhuma renovacao extra depois que a fila destravou", 1, refresher.calls.get())
        assertEquals(List(8) { "a-novo-1" }, resultados)
        assertEquals("a-novo-1", manager.currentAccessToken())
    }

    @Test
    fun `quem chega depois da renovacao reaproveita o token novo sem renovar de novo`() = runTest {
        val refresher = CountingRefresher({ n -> RefreshOutcome.Renewed("a-novo-$n", "r-novo-$n", 900) })
        val manager = manager(InMemoryTokenStore(tokens()), refresher)

        assertEquals("a-novo-1", manager.refreshAfterUnauthorized("a1"))
        // Chegou atrasada, ainda apresentando o token velho.
        assertEquals("a-novo-1", manager.refreshAfterUnauthorized("a1"))

        assertEquals(1, refresher.calls.get())
    }

    @Test
    fun `renovacao recusada derruba a sessao e limpa a guarda — o app volta ao login`() = runTest {
        val store = InMemoryTokenStore(tokens())
        val manager = manager(store, CountingRefresher({ RefreshOutcome.Rejected }))

        val resultado = manager.refreshAfterUnauthorized("a1")

        assertNull(resultado)
        assertEquals(SessionState.SignedOut, manager.state.value)
        assertNull("o par morto nao pode continuar gravado", store.load())
    }

    @Test
    fun `falha transitoria de rede nao derruba a sessao`() = runTest {
        // Wi-Fi dropping for three seconds must not cost a login: Unavailable
        // is deliberately different from Rejected.
        val store = InMemoryTokenStore(tokens())
        val manager = manager(store, CountingRefresher({ RefreshOutcome.Unavailable }))

        assertNull(manager.refreshAfterUnauthorized("a1"))

        assertTrue(manager.state.value is SessionState.SignedIn)
        assertEquals("a1", store.load()?.accessToken)
    }

    @Test
    fun `um token vencendo e renovado ANTES de sair a requisicao`() = runTest {
        val refresher = CountingRefresher({ RefreshOutcome.Renewed("a2", "r2", 900) })
        // 30s de validade: dentro da margem de EXPIRY_SKEW_MILLIS (60s).
        val manager = manager(InMemoryTokenStore(tokens(ttlSeconds = 30)), refresher)

        assertEquals("a2", manager.accessTokenForRequest())
        assertEquals(1, refresher.calls.get())
    }

    @Test
    fun `um token com folga nao dispara renovacao nenhuma`() = runTest {
        val refresher = CountingRefresher({ RefreshOutcome.Renewed("a2", "r2", 900) })
        val manager = manager(InMemoryTokenStore(tokens(ttlSeconds = 900)), refresher)

        assertEquals("a1", manager.accessTokenForRequest())
        assertEquals(0, refresher.calls.get())
    }

    @Test
    fun `sem sessao nao ha o que renovar`() = runTest {
        val refresher = CountingRefresher({ RefreshOutcome.Renewed("a2", "r2", 900) })
        val manager = manager(InMemoryTokenStore(), refresher)

        assertNull(manager.accessTokenForRequest())
        assertNull(manager.refreshAfterUnauthorized(null))
        assertEquals(0, refresher.calls.get())
    }

    @Test
    fun `signOut apaga a guarda e o token publicado`() {
        val published = mutableListOf<String?>()
        val store = InMemoryTokenStore(tokens())
        val manager = manager(store, CountingRefresher({ RefreshOutcome.Unavailable }), published)

        manager.signOut()

        assertNull(store.load())
        assertNull(manager.currentAccessToken())
        assertEquals(SessionState.SignedOut, manager.state.value)
        assertNull(published.last())
    }

    @Test
    fun `por padrao o access token e espelhado em ApiClient — o canal que midia e o socket do WhatsApp leem`() {
        // Without this mirror, MediaNetwork and OkHttpWhatsAppWsFactory would
        // go on reading null and sending requests with no credential -- which
        // was exactly the previous state (the comments in those files admitted
        // as much in writing).
        val manager = SessionManager(
            tokenStore = InMemoryTokenStore(),
            refresher = CountingRefresher({ RefreshOutcome.Unavailable }),
            now = { clock },
        )

        manager.establish("token-de-sessao", "r1", 900)
        assertEquals("token-de-sessao", ApiClient.accessToken)

        manager.signOut()
        assertNull(ApiClient.accessToken)
    }
}
