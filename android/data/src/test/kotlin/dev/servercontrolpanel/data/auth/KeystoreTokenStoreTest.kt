package dev.servercontrolpanel.data.auth

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Proves that [KeystoreTokenStore] fails closed. Robolectric has no `AndroidKeyStore` provider, so
 * these cases run the real degradation path. Unlike [dev.servercontrolpanel.data.config.EncryptedServerConfigStore],
 * which falls back to plaintext `SharedPreferences`, tokens must never reach disk.
 */
@RunWith(RobolectricTestRunner::class)
class KeystoreTokenStoreTest {

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    private val tokens = SessionTokens(
        accessToken = "secret-access-token-must-not-leak",
        refreshToken = "secret-refresh-token-must-not-leak",
        expiresAtEpochMillis = 1_800_000_000_000L,
    )

    @Test
    fun `no token reaches disk when the Keystore fails`() {
        val store = KeystoreTokenStore(context)

        store.save(tokens)

        val leaked = context.applicationInfo.dataDir
            ?.let(::File)
            ?.walkTopDown()
            ?.filter { it.isFile }
            ?.filter { file ->
                val text = runCatching { file.readText() }.getOrDefault("")
                text.contains(tokens.accessToken) || text.contains(tokens.refreshToken)
            }
            ?.map { it.path }
            ?.toList()
            .orEmpty()

        assertTrue(
            "token written in plaintext to app storage: $leaked, KeystoreTokenStore must " +
                "degrade to memory, never to plaintext SharedPreferences",
            leaked.isEmpty(),
        )
    }

    @Test
    fun `with the Keystore unavailable the store reports itself as not persistent`() {
        val store = KeystoreTokenStore(context)

        assertFalse(
            "isPersistent must be false so the UI can warn that the session ends on the next boot",
            store.isPersistent,
        )
    }

    @Test
    fun `the session stays usable in the current process without a Keystore`() {
        // Fail-closed still works in-process; only surviving a restart is lost.
        val store = KeystoreTokenStore(context)

        store.save(tokens)

        assertEquals(tokens, store.load())
    }

    @Test
    fun `a new instance does not see the previous session when storage is memory only`() {
        // A fresh instance over the same Context stands in for reopening the app.
        KeystoreTokenStore(context).save(tokens)

        assertNull(KeystoreTokenStore(context).load())
    }

    @Test
    fun `clear does not crash when storage degraded to memory`() {
        val store = KeystoreTokenStore(context)
        store.save(tokens)

        store.clear()

        assertNull(store.load())
    }
}
