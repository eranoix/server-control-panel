package com.vpsmanager.feature.auth

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.vpsmanager.core.model.ServerConfig
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.data.config.ServerConfigStore
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [ServerSetupScreen] under Robolectric -- never composed before
 * this. Stateless except for its own local text-field state, so this drives
 * it end to end through a real [ServerConfigRepository] backed by an
 * in-memory [ServerConfigStore] fake (that interface is the established fake
 * seam -- see `EncryptedServerConfigStoreTest`), never a mocked repository.
 */
@RunWith(RobolectricTestRunner::class)
class ServerSetupScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private class InMemoryServerConfigStore(private var config: ServerConfig? = null) : ServerConfigStore {
        override fun load(): ServerConfig? = config
        override fun save(config: ServerConfig) {
            this.config = config
        }
        override fun clear() {
            config = null
        }
    }

    @Test
    fun `a valid https url is accepted and fires onConfigured`() {
        var configured = false
        val repository = ServerConfigRepository(InMemoryServerConfigStore())
        composeRule.setContent {
            ServerSetupScreen(serverConfigRepository = repository, onConfigured = { configured = true })
        }

        composeRule.onNodeWithText("https://your-server.example.com").performTextInput("https://vpsmanager.example.com")
        composeRule.onNodeWithText("Continue").performClick()

        assert(configured) { "expected onConfigured to fire for a valid https url" }
        assert(repository.currentBaseUrl() == "https://vpsmanager.example.com")
    }

    @Test
    fun `an invalid url surfaces the rejection reason instead of navigating on`() {
        var configured = false
        val repository = ServerConfigRepository(InMemoryServerConfigStore())
        composeRule.setContent {
            ServerSetupScreen(serverConfigRepository = repository, onConfigured = { configured = true })
        }

        composeRule.onNodeWithText("https://your-server.example.com").performTextInput("not-a-url")
        composeRule.onNodeWithText("Continue").performClick()

        composeRule.onNodeWithText("Enter a full address, with https:// and the server's domain.").assertExists()
        assert(!configured) { "onConfigured must not fire for a rejected url" }
    }

    @Test
    fun `an already-configured device blocks a repoint attempt instead of silently switching servers`() {
        val repository = ServerConfigRepository(InMemoryServerConfigStore())
        repository.configure(rawUrl = "https://original.example.com")
        composeRule.setContent {
            ServerSetupScreen(serverConfigRepository = repository, onConfigured = {})
        }

        composeRule.onNodeWithText("https://your-server.example.com").performTextInput("https://attacker.example.com")
        composeRule.onNodeWithText("Continue").performClick()

        composeRule.onNodeWithText(
            "This app is already set up for https://original.example.com. " +
                "Remove the current setup before switching servers.",
        ).assertExists()
        assert(repository.currentBaseUrl() == "https://original.example.com")
    }
}
