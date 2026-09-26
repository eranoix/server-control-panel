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
 * Pins the session behaviour every screen depends on: storing the token pair, renewing
 * through a single queue, and tearing the session down when renewal is impossible.
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
    fun `a login establishes the session and publishes the access token`() {
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
    fun `a stored session is adopted on construction so the app reopens signed in`() {
        val manager = manager(InMemoryTokenStore(tokens()), CountingRefresher({ RefreshOutcome.Unavailable }))

        assertEquals("a1", manager.currentAccessToken())
        assertTrue(manager.state.value is SessionState.SignedIn)
    }

    @Test
    fun `without persistent storage the app opens signed out, the KeystoreTokenStore fail-closed case`() {
        val manager = manager(InMemoryTokenStore(), CountingRefresher({ RefreshOutcome.Unavailable }))

        assertEquals(SessionState.SignedOut, manager.state.value)
        assertNull(manager.currentAccessToken())
    }

    @Test
    fun `concurrent 401 responses trigger a single renewal`() = runTest {
        // The BFF rotates refresh tokens, so concurrent renewals would kill the session.
        // The gate holds the first renewal until all callers arrive, otherwise the test
        // would pass even without a queue.
        val gate = CompletableDeferred<Unit>()
        val refresher = CountingRefresher(
            outcome = { n -> RefreshOutcome.Renewed("a-new-$n", "r-new-$n", 900) },
            gate = gate,
        )
        val manager = manager(InMemoryTokenStore(tokens()), refresher)

        val requests = List(8) { async { manager.refreshAfterUnauthorized("a1") } }
        // All 8 are now blocked (one on the gate, seven on the mutex), none finished.
        advanceUntilIdle()
        assertEquals("one renewal per expiry, never one per call", 1, refresher.calls.get())

        gate.complete(Unit)
        val results = requests.awaitAll()

        assertEquals("no extra renewal after the queue was released", 1, refresher.calls.get())
        assertEquals(List(8) { "a-new-1" }, results)
        assertEquals("a-new-1", manager.currentAccessToken())
    }

    @Test
    fun `a late caller reuses the new token without renewing again`() = runTest {
        val refresher = CountingRefresher({ n -> RefreshOutcome.Renewed("a-new-$n", "r-new-$n", 900) })
        val manager = manager(InMemoryTokenStore(tokens()), refresher)

        assertEquals("a-new-1", manager.refreshAfterUnauthorized("a1"))
        // A late call still presenting the old token.
        assertEquals("a-new-1", manager.refreshAfterUnauthorized("a1"))

        assertEquals(1, refresher.calls.get())
    }

    @Test
    fun `a rejected renewal ends the session and clears storage`() = runTest {
        val store = InMemoryTokenStore(tokens())
        val manager = manager(store, CountingRefresher({ RefreshOutcome.Rejected }))

        val result = manager.refreshAfterUnauthorized("a1")

        assertNull(result)
        assertEquals(SessionState.SignedOut, manager.state.value)
        assertNull("the dead token pair must not stay stored", store.load())
    }

    @Test
    fun `a transient network failure does not end the session`() = runTest {
        // A brief network drop must not cost a login: Unavailable differs from Rejected.
        val store = InMemoryTokenStore(tokens())
        val manager = manager(store, CountingRefresher({ RefreshOutcome.Unavailable }))

        assertNull(manager.refreshAfterUnauthorized("a1"))

        assertTrue(manager.state.value is SessionState.SignedIn)
        assertEquals("a1", store.load()?.accessToken)
    }

    @Test
    fun `an expiring token is renewed before the request goes out`() = runTest {
        val refresher = CountingRefresher({ RefreshOutcome.Renewed("a2", "r2", 900) })
        // 30s of validity is inside the EXPIRY_SKEW_MILLIS margin (60s).
        val manager = manager(InMemoryTokenStore(tokens(ttlSeconds = 30)), refresher)

        assertEquals("a2", manager.accessTokenForRequest())
        assertEquals(1, refresher.calls.get())
    }

    @Test
    fun `a token with time left triggers no renewal`() = runTest {
        val refresher = CountingRefresher({ RefreshOutcome.Renewed("a2", "r2", 900) })
        val manager = manager(InMemoryTokenStore(tokens(ttlSeconds = 900)), refresher)

        assertEquals("a1", manager.accessTokenForRequest())
        assertEquals(0, refresher.calls.get())
    }

    @Test
    fun `without a session there is nothing to renew`() = runTest {
        val refresher = CountingRefresher({ RefreshOutcome.Renewed("a2", "r2", 900) })
        val manager = manager(InMemoryTokenStore(), refresher)

        assertNull(manager.accessTokenForRequest())
        assertNull(manager.refreshAfterUnauthorized(null))
        assertEquals(0, refresher.calls.get())
    }

    @Test
    fun `signOut clears storage and the published token`() {
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
    fun `by default the access token is mirrored to ApiClient for media and the WhatsApp socket`() {
        // MediaNetwork and OkHttpWhatsAppWsFactory read the token from ApiClient.
        val manager = SessionManager(
            tokenStore = InMemoryTokenStore(),
            refresher = CountingRefresher({ RefreshOutcome.Unavailable }),
            now = { clock },
        )

        manager.establish("session-token", "r1", 900)
        assertEquals("session-token", ApiClient.accessToken)

        manager.signOut()
        assertNull(ApiClient.accessToken)
    }
}
