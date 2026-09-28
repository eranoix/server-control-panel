package dev.servercontrolpanel.data.config

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import dev.servercontrolpanel.core.model.ServerConfig
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class EncryptedServerConfigStoreTest {

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test
    fun `load never throws when the Keystore is unavailable, and falls back to an unconfigured state`() {
        val store = EncryptedServerConfigStore(context)

        val config = store.load()

        assertNull("no config has been saved yet -- must degrade to null, not crash", config)
    }

    @Test
    fun `the specific throws-once-accessed-twice shape - repeated access after a failed Keystore init never re-throws`() {
        val store = EncryptedServerConfigStore(context)

        assertNull(store.load())
        assertNull(store.load())
        store.save(ServerConfig(baseUrl = "https://panel.example.com"))
        val reloaded = store.load()

        assertEquals("https://panel.example.com", reloaded?.baseUrl)
    }

    @Test
    fun `save and load round-trip through the unencrypted fallback store`() {
        val store = EncryptedServerConfigStore(context)
        val config = ServerConfig(baseUrl = "https://panel.example.com", allowInsecureHttp = true)

        store.save(config)
        val reloaded = store.load()

        assertEquals(config, reloaded)
    }

    @Test
    fun `clear removes a previously saved config`() {
        val store = EncryptedServerConfigStore(context)
        store.save(ServerConfig(baseUrl = "https://panel.example.com"))

        store.clear()

        assertNull(store.load())
    }

    @Test
    fun `ServerConfigRepository's eager constructor read of store load never propagates`() {
        val repository = ServerConfigRepository(EncryptedServerConfigStore(context))

        assertNull(repository.currentConfig())
        assertNull(repository.currentBaseUrl())
    }

    @Test
    fun `by lazy does not cache a thrown initializer - the root cause this store's fix works around`() {
        var attempts = 0
        val brokenLazy by lazy {
            attempts++
            throw IllegalStateException("keystore unavailable, attempt $attempts")
        }

        val firstFailure = runCatching { brokenLazy }.exceptionOrNull()
        val secondFailure = runCatching { brokenLazy }.exceptionOrNull()

        assertTrue(firstFailure is IllegalStateException)
        assertTrue(secondFailure is IllegalStateException)
        assertEquals(2, attempts)
        assertEquals("keystore unavailable, attempt 1", firstFailure?.message)
        assertEquals("keystore unavailable, attempt 2", secondFailure?.message)
    }
}
