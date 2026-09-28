package dev.servercontrolpanel.feature.auth

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import dev.servercontrolpanel.data.auth.PairingPayload
import dev.servercontrolpanel.data.auth.PairingRepository
import dev.servercontrolpanel.data.auth.PasskeyRepository
import dev.servercontrolpanel.core.model.ServerConfig
import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.data.config.ServerConfigStore
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class PasskeyRegisterFlowTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun viewModel(): PasskeyRegisterViewModel {
        val store = object : ServerConfigStore {
            private var config: ServerConfig? = null
            override fun load(): ServerConfig? = config
            override fun save(config: ServerConfig) {
                this.config = config
            }
            override fun clear() {
                config = null
            }
        }
        val serverConfigRepository = ServerConfigRepository(store)
        return PasskeyRegisterViewModel(
            serverConfigRepository = serverConfigRepository,
            pairingRepository = PairingRepository(),
            passkeyRepository = PasskeyRepository(serverConfigRepository),
        )
    }

    @Test
    fun `initial scanning state renders the camera screen and the manual-setup escape hatch`() {
        composeRule.setContent { PasskeyRegisterFlow(onManualSetupRequested = {}, viewModel = viewModel()) }

        composeRule.onNodeWithText("Set up manually").assertExists()
    }

    @Test
    fun `a rejected server url surfaces a retryable error, without ever reaching the passkey ceremony`() {
        val vm = viewModel()
        val context = ApplicationProvider.getApplicationContext<android.app.Application>()
        composeRule.setContent { PasskeyRegisterFlow(onManualSetupRequested = {}, viewModel = vm) }

        vm.onPairingScanned(context, PairingPayload(ticket = "t", serverUrl = "http://example.com"))
        composeRule.waitForIdle()

        composeRule.onNodeWithText(
            "This server needs https:// (http:// is only allowed for local development).",
        ).assertExists()
        composeRule.onNodeWithText("Scan again").performClick()
        composeRule.onNodeWithText("Set up manually").assertExists()
    }
}
