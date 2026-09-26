package com.vpsmanager.data.auth

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
 * Proof that [KeystoreTokenStore] FAILS CLOSED.
 *
 * Robolectric has no `AndroidKeyStore` `java.security.Provider` at all —
 * `MasterKey.Builder(context).build()` throws
 * `java.security.KeyStoreException: AndroidKeyStore not found` every time (the
 * same fact [com.vpsmanager.data.config.EncryptedServerConfigStoreTest] already
 * documents). In other words: these cases exercise the REAL degradation path
 * against a genuinely unavailable Keystore, not a mock standing in for one.
 *
 * What is being locked down here is the deliberate difference from the
 * neighbouring [com.vpsmanager.data.config.EncryptedServerConfigStore], which
 * under the SAME conditions writes to `SharedPreferences` in the clear. Copying
 * that fallback over here would break the case below proving that no token
 * reaches disk when the Keystore fails.
 */
@RunWith(RobolectricTestRunner::class)
class KeystoreTokenStoreTest {

    private val context: Context get() = ApplicationProvider.getApplicationContext()

    private val tokens = SessionTokens(
        accessToken = "access-token-secreto-nao-pode-vazar",
        refreshToken = "refresh-token-secreto-nao-pode-vazar",
        expiresAtEpochMillis = 1_800_000_000_000L,
    )

    @Test
    fun `nenhum token vai para disco quando o Keystore falha`() {
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
            "token gravado em claro no disco do app: $leaked -- KeystoreTokenStore tem que " +
                "degradar para memoria, nunca para SharedPreferences em claro",
            leaked.isEmpty(),
        )
    }

    @Test
    fun `com o Keystore indisponivel a guarda se declara nao persistente`() {
        val store = KeystoreTokenStore(context)

        assertFalse(
            "isPersistent tem que ser falso para a UI poder avisar que a sessao morre no proximo boot",
            store.isPersistent,
        )
    }

    @Test
    fun `a sessao continua utilizavel no processo atual mesmo sem Keystore`() {
        // Fail-closed must not become fail-useless: inside the process the
        // session works normally; what it loses is surviving the next boot.
        val store = KeystoreTokenStore(context)

        store.save(tokens)

        assertEquals(tokens, store.load())
    }

    @Test
    fun `uma instancia nova nao enxerga a sessao da anterior quando a guarda e so memoria`() {
        // The in-test equivalent of reopening the app: a fresh instance over
        // the same Context. Had it persisted to disk, this would now find the
        // tokens -- and that would be exactly the security failure this file
        // exists to prevent.
        KeystoreTokenStore(context).save(tokens)

        assertNull(KeystoreTokenStore(context).load())
    }

    @Test
    fun `clear nao explode quando a guarda degradou para memoria`() {
        val store = KeystoreTokenStore(context)
        store.save(tokens)

        store.clear()

        assertNull(store.load())
    }
}
