package com.vpsmanager.feature.auth

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import com.vpsmanager.data.config.ServerConfigRepository

/**
 * The three screens a device with NO session may need, and how one navigates
 * between them.
 */
enum class AuthGateStep {
    /** Scan the panel's QR: configures the server AND registers the passkey. */
    Pairing,

    /** Type the server address by hand — the way out when there is no camera. */
    ManualSetup,

    /** Sign in: passkey or username+password. */
    Login,
}

/**
 * Decides where a device with no session begins.
 *
 * While there was no server configured, "first screen" and "no session" were
 * the same question and pairing was always the beginning. With the server
 * baked into the build (`BuildConfig.DEFAULT_SERVER_URL`, seeded on the first
 * boot), they stopped being: a freshly installed device ALREADY has an
 * address and does not yet have a single credential. In that case the right
 * beginning is the login — which offers pairing as one of its paths — rather
 * than throwing the operator straight at the camera without saying why.
 */
internal fun initialAuthGateStep(serverConfigured: Boolean): AuthGateStep =
    if (serverConfigured) AuthGateStep.Login else AuthGateStep.Pairing

/**
 * Everything that happens before a session exists, in one place.
 * `MainActivity` only needs to know "is there a session?" — navigating
 * between pairing, configuring by hand and signing in is internal to here.
 *
 * There is no dead end: from any one of the three steps you can reach the
 * other two. In particular, the `pending_approval` that ends the pairing (the
 * passkey is born inert, see `internal/mobilebff/auth_passkey.go`) leads back
 * to the login, where the operator can sign in with a password while waiting
 * for the approval on the panel.
 */
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
