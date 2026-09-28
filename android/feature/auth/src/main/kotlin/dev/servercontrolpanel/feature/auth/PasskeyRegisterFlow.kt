package dev.servercontrolpanel.feature.auth

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
import dev.servercontrolpanel.data.auth.PairingPayload
import dev.servercontrolpanel.data.auth.PairingRepository
import dev.servercontrolpanel.data.auth.PairingResult
import dev.servercontrolpanel.data.auth.PasskeyError
import dev.servercontrolpanel.data.auth.PasskeyRepository
import dev.servercontrolpanel.data.auth.RegistrationResult
import dev.servercontrolpanel.data.config.ConfigureServerResult
import dev.servercontrolpanel.data.config.EncryptedServerConfigStore
import dev.servercontrolpanel.data.config.ServerConfigRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface PasskeyRegisterUiState {
    data object Scanning : PasskeyRegisterUiState
    data object Processing : PasskeyRegisterUiState
    data object PendingApproval : PasskeyRegisterUiState
    data class Error(val message: String, val retryable: Boolean) : PasskeyRegisterUiState
}

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

    fun retry() {
        _uiState.value = PasskeyRegisterUiState.Scanning
    }
}

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

private fun PasskeyError.isRetryable(): Boolean = this !is PasskeyError.RpIdMismatch

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
                if (onLoginRequested != null) {
                    TextButton(onClick = onLoginRequested) {
                        Text(text = "Go to sign-in")
                    }
                }
            }
        }
    }
}
