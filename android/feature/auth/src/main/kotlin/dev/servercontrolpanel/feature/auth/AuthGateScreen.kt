package dev.servercontrolpanel.feature.auth

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import dev.servercontrolpanel.data.config.ServerConfigRepository

enum class AuthGateStep {
    Pairing,

    ManualSetup,

    Login,
}

internal fun initialAuthGateStep(serverConfigured: Boolean): AuthGateStep =
    if (serverConfigured) AuthGateStep.Login else AuthGateStep.Pairing

@Composable
fun AuthGateScreen(
    serverConfigRepository: ServerConfigRepository,
    modifier: Modifier = Modifier,
    initialStep: AuthGateStep = initialAuthGateStep(serverConfigRepository.currentBaseUrl() != null),
) {
    var step by remember { mutableStateOf(initialStep) }

    when (step) {
        AuthGateStep.Login -> LoginScreen(
            modifier = modifier,
            onPairDeviceRequested = { step = AuthGateStep.Pairing },
        )
        AuthGateStep.Pairing -> PasskeyRegisterFlow(
            modifier = modifier,
            onManualSetupRequested = { step = AuthGateStep.ManualSetup },
            onLoginRequested = { step = AuthGateStep.Login },
        )
        AuthGateStep.ManualSetup -> ServerSetupScreen(
            modifier = modifier,
            serverConfigRepository = serverConfigRepository,
            onConfigured = { step = AuthGateStep.Login },
        )
    }
}
