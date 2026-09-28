package dev.servercontrolpanel.feature.auth

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import dev.servercontrolpanel.core.model.ServerConfig
import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.data.config.ServerConfigStore
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

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

        composeRule.onNodeWithText("https://your-server.example.com").performTextInput("https://panel.example.com")
        composeRule.onNodeWithText("Continue").performClick()

        assert(configured) { "expected onConfigured to fire for a valid https url" }
        assert(repository.currentBaseUrl() == "https://panel.example.com")
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
