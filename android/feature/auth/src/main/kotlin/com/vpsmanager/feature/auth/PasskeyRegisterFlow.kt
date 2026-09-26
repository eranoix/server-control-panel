package com.vpsmanager.feature.auth

import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.remember
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import com.vpsmanager.data.auth.PairingPayload
import com.vpsmanager.data.auth.PairingRepository
import com.vpsmanager.data.auth.PairingResult
import com.vpsmanager.data.auth.PasskeyError
import com.vpsmanager.data.auth.PasskeyRepository
import com.vpsmanager.data.auth.RegistrationResult
import com.vpsmanager.data.config.ConfigureServerResult
import com.vpsmanager.data.config.EncryptedServerConfigStore
import com.vpsmanager.data.config.ServerConfigRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * State rendered by [PasskeyRegisterFlow]. [PendingApproval] is deliberately
 * a dead end here — this screen never polls `login/finish` to "wait out"
 * the approval. Registration and login are two separate ceremonies against
 * two separate endpoints (`register/finish` vs `login/finish`); a login
 * attempt belongs to the passkey-first login screen, which will naturally
 * see `pending_approval` on its own terms once the desktop approves this
 * device.
 */
sealed interface PasskeyRegisterUiState {
    data object Scanning : PasskeyRegisterUiState
    data object Processing : PasskeyRegisterUiState
    data object PendingApproval : PasskeyRegisterUiState
    data class Error(val message: String, val retryable: Boolean) : PasskeyRegisterUiState
}

/**
 * Drives the pair-to-registration chain end to end, starting from the
 * scanned QR envelope rather than a bare ticket: first
 * [ServerConfigRepository.configure] applies [PairingPayload.serverUrl] —
 * NEVER with `allowRepoint = true`, so a QR can configure a fresh install
 * but can never silently repoint an already-paired device at another host
 * (see [ConfigureServerResult.RepointBlocked]) — then
 * [PairingRepository.consume] (ticket -> `reg_token`, never itself a
 * credential — see [PairingResult.Authorized]) then
 * [PasskeyRepository.register] (`reg_token` -> Credential Manager ceremony ->
 * `pending_approval`). The `reg_token` never leaves this ViewModel.
 */
class PasskeyRegisterViewModel(
    private val serverConfigRepository: ServerConfigRepository,
    private val pairingRepository: PairingRepository,
    private val passkeyRepository: PasskeyRepository,
) : ViewModel() {

    private val _uiState = MutableStateFlow<PasskeyRegisterUiState>(PasskeyRegisterUiState.Scanning)
    val uiState: StateFlow<PasskeyRegisterUiState> = _uiState.asStateFlow()

    fun onPairingScanned(activityContext: Context, payload: PairingPayload) {
        _uiState.value = PasskeyRegisterUiState.Processing
        viewModelScope.launch {
            when (
                val configResult = serverConfigRepository.configure(
                    rawUrl = payload.serverUrl,
                    allowInsecureHttp = false,
                    allowRepoint = false,
                )
            ) {
                is ConfigureServerResult.Rejected -> {
                    _uiState.value = PasskeyRegisterUiState.Error(configResult.reason, retryable = true)
                    return@launch
                }
                is ConfigureServerResult.RepointBlocked -> {
                    _uiState.value = PasskeyRegisterUiState.Error(
                        message = "This app is already paired with ${configResult.currentBaseUrl}. " +
                            "Remove the current setup before scanning a QR code from another server.",
                        retryable = true,
                    )
                    return@launch
                }
                is ConfigureServerResult.Applied -> Unit
            }
            when (val pairing = pairingRepository.consume(payload.ticket)) {
                is PairingResult.Error -> {
                    _uiState.value = PasskeyRegisterUiState.Error(pairing.reason, retryable = true)
                }
                is PairingResult.Authorized -> {
                    when (val registration = passkeyRepository.register(activityContext, pairing.regToken)) {
                        RegistrationResult.PendingApproval -> {
                            _uiState.value = PasskeyRegisterUiState.PendingApproval
                        }
                        is RegistrationResult.Failed -> {
                            _uiState.value = PasskeyRegisterUiState.Error(
                                message = registration.error.message,
                                retryable = registration.error.isRetryable(),
                            )
                        }
                    }
                }
            }
        }
    }

    /** Returns to the QR scanner — used by every retryable error state. */
    fun retry() {
        _uiState.value = PasskeyRegisterUiState.Scanning
    }
}

/**
 * Builds the default [PasskeyRegisterViewModel] with a
 * [ServerConfigRepository] read from [LocalContext] — there is no DI
 * framework in this app, so composable call sites wire dependencies
 * themselves (see `VpsManagerApplication`'s own doc comment). The
 * `viewModel = ...` default parameter above remains a real injection
 * point: tests (and future call sites) can still pass their own
 * [PasskeyRegisterViewModel] directly instead of going through this.
 */
@Composable
private fun passkeyRegisterViewModel(): PasskeyRegisterViewModel {
    val context = LocalContext.current
    val factory = remember(context) {
        viewModelFactory {
            initializer {
                val serverConfigRepository = ServerConfigRepository(EncryptedServerConfigStore(context))
                PasskeyRegisterViewModel(
                    serverConfigRepository = serverConfigRepository,
                    pairingRepository = PairingRepository(),
                    passkeyRepository = PasskeyRepository(serverConfigRepository),
                )
            }
        }
    }
    return viewModel(factory = factory)
}

/**
 * A [RpIdMismatch][PasskeyError.RpIdMismatch] is a build/environment
 * misconfiguration, not a transient failure — rescanning the same QR code
 * against the same misconfigured app would fail the exact same way, so it is
 * the one error this screen does not offer a retry for.
 */
private fun PasskeyError.isRetryable(): Boolean = this !is PasskeyError.RpIdMismatch

/**
 * Composes [PairingScanScreen] with the registration ceremony it feeds:
 * scan -> configure server from the envelope -> consume ticket -> Credential
 * Manager ceremony -> distinct pending-approval / error states. Registration
 * success is never rendered as a logged-in state — see
 * [PasskeyRegisterUiState.PendingApproval].
 *
 * This is the first-run path for a device with no server yet (see
 * [AuthGateScreen]): [onManualSetupRequested], shown only during
 * [PasskeyRegisterUiState.Scanning], is the escape hatch to
 * [ServerSetupScreen] for a device that cannot scan (no camera, no admin
 * physically present with the panel) — manual entry is the fallback, never
 * the default.
 *
 * [onLoginRequested] closes the dead end this flow used to have: registration
 * ALWAYS ends in `pending_approval` (the passkey is born inert, see
 * `auth_passkey.go`) and, with no way out of here, the operator was left
 * staring at an informational card with nothing to tap. Now that same card
 * leads to the login screen, where they can get in by password while the
 * approval is outstanding.
 */
@Composable
fun PasskeyRegisterFlow(
    modifier: Modifier = Modifier,
    onManualSetupRequested: () -> Unit,
    onLoginRequested: (() -> Unit)? = null,
    viewModel: PasskeyRegisterViewModel = passkeyRegisterViewModel(),
) {
    val context = LocalContext.current
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    when (val current = state) {
        // The exits ("Set up manually" / "I already have access") are drawn
        // INSIDE PairingScanScreen: only it knows whether it is showing the
        // camera — where they stay discreet over the video — or a degraded
        // state, where they are the card's primary action. With the exits out
        // here, a white text button ended up over the degraded state's light
        // card, illegible.
        PasskeyRegisterUiState.Scanning -> PairingScanScreen(
            modifier = modifier,
            onPairingScanned = { payload -> viewModel.onPairingScanned(context, payload) },
            onManualSetupRequested = onManualSetupRequested,
            onLoginRequested = onLoginRequested,
        )
        PasskeyRegisterUiState.Processing -> ProcessingContent(modifier)
        PasskeyRegisterUiState.PendingApproval -> PendingApprovalContent(modifier, onLoginRequested)
        is PasskeyRegisterUiState.Error -> RegisterErrorContent(
            modifier = modifier,
            message = current.message,
            retryable = current.retryable,
            onRetry = viewModel::retry,
            onLoginRequested = onLoginRequested,
        )
    }
}

@Composable
private fun ProcessingContent(modifier: Modifier = Modifier) {
    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            CircularProgressIndicator()
            Text(text = "Registering the passkey on this device…")
        }
    }
}

@Composable
private fun PendingApprovalContent(modifier: Modifier = Modifier, onLoginRequested: (() -> Unit)? = null) {
    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Card {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(text = "Waiting for approval in the panel", style = MaterialTheme.typography.titleLarge)
                Text(
                    text = "The passkey was created on this device but cannot be used yet. In the " +
                        "panel, open Security → \"Paired devices (Android app)\" and tap " +
                        "Approve for this device.",
                )
                if (onLoginRequested != null) {
                    Text(
                        text = "Once approved, sign in with the passkey. Meanwhile, you can " +
                            "sign in with username and password.",
                    )
                    Button(onClick = onLoginRequested) {
                        Text(text = "Go to sign-in")
                    }
                }
            }
        }
    }
}

@Composable
private fun RegisterErrorContent(
    modifier: Modifier = Modifier,
    message: String,
    retryable: Boolean,
    onRetry: () -> Unit,
    onLoginRequested: (() -> Unit)? = null,
) {
    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Card {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Text(text = "Could not pair", style = MaterialTheme.typography.titleLarge)
                Text(text = message)
                if (retryable) {
                    Button(onClick = onRetry) {
                        Text(text = "Scan again")
                    }
                }
                // Even a NON-retryable error has to lead somewhere — without
                // this, an RpIdMismatch left the screen with no action at all.
                if (onLoginRequested != null) {
                    TextButton(onClick = onLoginRequested) {
                        Text(text = "Go to sign-in")
                    }
                }
            }
        }
    }
}
