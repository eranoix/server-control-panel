package com.vpsmanager.data.config

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.vpsmanager.core.model.ServerConfig
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Regression coverage for the crash that produced three failed install cycles on the operator's
 * phone: [EncryptedServerConfigStore] touches the Android Keystore
 * ([androidx.security.crypto.MasterKey.Builder.build]) synchronously the first time [prefs][EncryptedServerConfigStore]
 * is read, which [ServerConfigRepository]'s constructor does eagerly, inside
 * `Application.onCreate`, before any UI exists.
 *
 * Robolectric has no `AndroidKeyStore` `java.security.Provider` at all (confirmed empirically:
 * `MasterKey.Builder(context).build()` throws `java.security.KeyStoreException: AndroidKeyStore
 * not found` — a [GeneralSecurityException][java.security.GeneralSecurityException] — every
 * single time it runs here), so this suite exercises the *real* [EncryptedServerConfigStore.createPrefs]
 * fallback path against a genuinely unavailable Keystore, not a mock standing in for one.
 */
@RunWith(RobolectricTestRunner::class)
class EncryptedServerConfigStoreTest {

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test
    fun `load never throws when the Keystore is unavailable, and falls back to an unconfigured state`() {
        val store = EncryptedServerConfigStore(context)

        // Must not throw. Pre-fix, this exact call is where a `GeneralSecurityException` from
        // MasterKey.Builder().build() propagated out of ServerConfigRepository's constructor.
        val config = store.load()

        assertNull("no config has been saved yet -- must degrade to null, not crash", config)
    }

    @Test
    fun `the specific throws-once-accessed-twice shape - repeated access after a failed Keystore init never re-throws`() {
        // This is the exact mechanism that made the bug fatal on every future launch: Kotlin's
        // `by lazy { }` does NOT cache a thrown exception, so a naive
        // `private val prefs by lazy { createEncryptedPrefs() }` (no try/catch inside the
        // initializer) re-runs -- and re-throws -- on every subsequent access, not just the
        // first. Calling load()/save() several times in a row on the SAME store instance proves
        // the fix (the try/catch lives INSIDE createPrefs(), so the fallback result itself gets
        // cached by `lazy`, and the broken Keystore is never touched again).
        val store = EncryptedServerConfigStore(context)

        assertNull(store.load())
        assertNull(store.load())
        store.save(ServerConfig(baseUrl = "https://vpsm.example.com"))
        val reloaded = store.load()

        assertEquals("https://vpsm.example.com", reloaded?.baseUrl)
    }

    @Test
    fun `save and load round-trip through the unencrypted fallback store`() {
        val store = EncryptedServerConfigStore(context)
        val config = ServerConfig(baseUrl = "https://vpsm.example.com", allowInsecureHttp = true)

        store.save(config)
        val reloaded = store.load()

        assertEquals(config, reloaded)
    }

    @Test
    fun `clear removes a previously saved config`() {
        val store = EncryptedServerConfigStore(context)
        store.save(ServerConfig(baseUrl = "https://vpsm.example.com"))

        store.clear()

        assertNull(store.load())
    }

    @Test
    fun `ServerConfigRepository's eager constructor read of store load never propagates`() {
        // This is the literal crash site: `ServerConfigRepository`'s constructor calls
        // `store.load()` in a property initializer, run the very first time the repository is
        // touched -- inside VpsManagerApplication.onCreate, before any UI exists to show a
        // recovery path.
        val repository = ServerConfigRepository(EncryptedServerConfigStore(context))

        assertNull(repository.currentConfig())
        assertNull(repository.currentBaseUrl())
    }

    @Test
    fun `by lazy does not cache a thrown initializer - the root cause this store's fix works around`() {
        // Standalone proof of the Kotlin-level mechanic the class doc references: a `lazy {}`
        // whose initializer throws re-invokes that initializer on every subsequent access,
        // rather than caching (and only ever re-throwing) the original failure once.
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
