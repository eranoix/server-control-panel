package dev.servercontrolpanel.feature.auth

import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import dev.servercontrolpanel.data.auth.AppSession
import dev.servercontrolpanel.data.auth.LoginResult
import dev.servercontrolpanel.data.auth.PasskeyError
import dev.servercontrolpanel.data.auth.PasskeyLoginSource
import dev.servercontrolpanel.data.auth.PasskeyRepository
import dev.servercontrolpanel.data.auth.PasswordLoginRepository
import dev.servercontrolpanel.data.auth.PasswordLoginResult
import dev.servercontrolpanel.data.auth.PasswordLoginSource
import dev.servercontrolpanel.data.auth.SessionManager
import dev.servercontrolpanel.data.config.EncryptedServerConfigStore
import dev.servercontrolpanel.data.config.ServerConfigRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class LoginMode {
    Choice,

    Password,
}

data class LoginUiState(
    val mode: LoginMode = LoginMode.Choice,
    val busy: Boolean = false,
    val error: String? = null,
    val pendingApproval: Boolean = false,
    val username: String = "",
    val password: String = "",
    val totpRequired: Boolean = false,
    val totpCode: String = "",
    val sessionPersistent: Boolean = true,
    val serverUrl: String? = null,
)

class LoginViewModel(
    private val session: SessionManager,
    private val passkeyRepository: PasskeyLoginSource,
    private val passwordLoginRepository: PasswordLoginSource,
    serverUrl: String?,
) : ViewModel() {

    private val _uiState = MutableStateFlow(
        LoginUiState(sessionPersistent = session.isSessionPersistent, serverUrl = serverUrl),
    )
    val uiState: StateFlow<LoginUiState> = _uiState.asStateFlow()

    fun loginWithPasskey(activityContext: Context) {
        _uiState.update { it.copy(busy = true, error = null, pendingApproval = false) }
        viewModelScope.launch {
            when (val result = passkeyRepository.login(activityContext)) {
                is LoginResult.Success -> {
                    session.establish(result.accessToken, result.refreshToken, result.expiresIn)
                    _uiState.update { it.copy(busy = false) }
                }
                LoginResult.PendingApproval ->
                    _uiState.update { it.copy(busy = false, pendingApproval = true, error = null) }
                is LoginResult.Failed ->
                    _uiState.update { it.copy(busy = false, error = result.error.helpText()) }
            }
        }
    }

    fun showPasswordForm() = _uiState.update {
        it.copy(mode = LoginMode.Password, error = null, pendingApproval = false)
    }

    fun backToChoices() = _uiState.update {
        it.copy(mode = LoginMode.Choice, error = null, totpRequired = false, totpCode = "")
    }

    fun onUsernameChange(value: String) = _uiState.update {
        if (value == it.username) it else it.copy(username = value, totpRequired = false, totpCode = "")
    }

    fun onPasswordChange(value: String) = _uiState.update { it.copy(password = value) }

    fun onTotpChange(value: String) = _uiState.update { it.copy(totpCode = value) }

    fun submitPassword() {
        val snapshot = _uiState.value
        if (snapshot.username.isBlank() || snapshot.password.isBlank()) {
            _uiState.update { it.copy(error = "Enter your username and password.") }
            return
        }
        if (snapshot.totpRequired && snapshot.totpCode.isBlank()) {
            _uiState.update { it.copy(error = "Enter the verification code to continue.") }
            return
        }
        _uiState.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            val result = passwordLoginRepository.login(
                username = snapshot.username,
                password = snapshot.password,
                totpCode = snapshot.totpCode.takeIf { snapshot.totpRequired },
            )
            when (result) {
                is PasswordLoginResult.Success -> {
                    session.establish(result.accessToken, result.refreshToken, result.expiresInSeconds)
                    _uiState.update {
                        it.copy(busy = false, password = "", totpCode = "", totpRequired = false)
                    }
                }
                PasswordLoginResult.TotpRequired ->
                    _uiState.update { it.copy(busy = false, totpRequired = true, error = null) }
                is PasswordLoginResult.InvalidCode ->
                    _uiState.update {
                        it.copy(busy = false, totpRequired = true, totpCode = "", error = result.reason)
                    }
                is PasswordLoginResult.Failed ->
                    _uiState.update { it.copy(busy = false, error = result.reason) }
            }
        }
    }
}

internal fun PasskeyError.helpText(): String = when (this) {
    PasskeyError.NoPasskeyAvailable ->
        "$message Tap \"Pair this device\" to register a passkey by QR code, " +
            "or sign in with username and password."
    is PasskeyError.RpIdMismatch ->
        "$message This build points at another domain — reinstall the app from the correct server."
    else -> message
}

@Composable
fun LoginScreen(
    modifier: Modifier = Modifier,
    onPairDeviceRequested: () -> Unit,
    viewModel: LoginViewModel = defaultLoginViewModel(),
) {
    val context = LocalContext.current
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    Column(
        modifier = modifier
            .fillMaxSize()
            .safeDrawingPadding()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(text = "Sign in to Server Control Panel", style = MaterialTheme.typography.headlineSmall)
        state.serverUrl?.let { url ->
            Text(text = "Server: $url", style = MaterialTheme.typography.bodySmall)
        }

        if (!state.sessionPersistent) {
            WarningCard(
                title = "The session will not be remembered",
                body = "This device has no key vault available, so the app keeps the " +
                    "session in memory only — no token is written to disk unencrypted. " +
                    "You will need to sign in again when the app is closed.",
            )
        }

        if (state.pendingApproval) {
            PendingApprovalCard(serverUrl = state.serverUrl)
        }

        state.error?.let { message ->
            WarningCard(title = "Could not sign in", body = message)
        }

        if (state.busy) {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                CircularProgressIndicator()
                Text(text = "Authenticating…")
            }
        }

        when (state.mode) {
            LoginMode.Choice -> ChoiceContent(
                busy = state.busy,
                onPasskey = { viewModel.loginWithPasskey(context) },
                onPassword = viewModel::showPasswordForm,
                onPair = onPairDeviceRequested,
            )
            LoginMode.Password -> PasswordContent(
                state = state,
                onUsernameChange = viewModel::onUsernameChange,
                onPasswordChange = viewModel::onPasswordChange,
                onTotpChange = viewModel::onTotpChange,
                onSubmit = viewModel::submitPassword,
                onBack = viewModel::backToChoices,
            )
        }
    }
}

@Composable
private fun ChoiceContent(busy: Boolean, onPasskey: () -> Unit, onPassword: () -> Unit, onPair: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.fillMaxWidth()) {
        Button(onClick = onPasskey, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Sign in with passkey")
        }
        OutlinedButton(onClick = onPassword, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Sign in with username and password")
        }
        TextButton(onClick = onPair, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Pair this device (QR code)")
        }
        Text(
            text = "First time on this device? Pair using the panel's QR code to register a " +
                "passkey. An administrator must approve the device in the panel before the first " +
                "passkey sign-in.",
            style = MaterialTheme.typography.bodySmall,
        )
    }
}

@Composable
private fun PasswordContent(
    state: LoginUiState,
    onUsernameChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onTotpChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onBack: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.fillMaxWidth()) {
        OutlinedTextField(
            value = state.username,
            onValueChange = onUsernameChange,
            enabled = !state.busy,
            singleLine = true,
            label = { Text(text = "Username or email") },
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = state.password,
            onValueChange = onPasswordChange,
            enabled = !state.busy,
            singleLine = true,
            label = { Text(text = "Password") },
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
            modifier = Modifier.fillMaxWidth(),
        )
        if (state.totpRequired) {
            Text(
                text = "Username and password are correct. Two-step verification is still needed: enter the 6 " +
                    "digits from your authenticator app (they change every 30 seconds) or a backup code.",
                style = MaterialTheme.typography.bodySmall,
            )
            OutlinedTextField(
                value = state.totpCode,
                onValueChange = onTotpChange,
                enabled = !state.busy,
                singleLine = true,
                label = { Text(text = "Verification code") },
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.NumberPassword,
                    imeAction = ImeAction.Done,
                ),
                modifier = Modifier.fillMaxWidth(),
            )
        }
        Button(onClick = onSubmit, enabled = !state.busy, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Sign in")
        }
        TextButton(onClick = onBack, enabled = !state.busy, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Back")
        }
    }
}

@Composable
private fun PendingApprovalCard(serverUrl: String?) {
    Card {
        Column(modifier = Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(text = "Waiting for approval in the panel", style = MaterialTheme.typography.titleMedium)
            Text(
                text = "This device's passkey exists but cannot sign in yet. In the panel" +
                    (serverUrl?.let { " ($it)" } ?: "") +
                    ", open Security → \"Paired devices (Android app)\" and tap Approve " +
                    "for this device. Then come back here and tap \"Sign in with passkey\" again.",
            )
            Text(
                text = "Meanwhile, you can sign in with username and password.",
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}

@Composable
private fun WarningCard(title: String, body: String) {
    Card {
        Column(modifier = Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(text = title, style = MaterialTheme.typography.titleMedium)
            Text(text = body)
        }
    }
}

@Composable
private fun defaultLoginViewModel(): LoginViewModel {
    val context = LocalContext.current
    val factory = remember(context) {
        viewModelFactory {
            initializer {
                val serverConfigRepository = ServerConfigRepository(EncryptedServerConfigStore(context))
                LoginViewModel(
                    session = AppSession.get(context),
                    passkeyRepository = PasskeyRepository(serverConfigRepository),
                    passwordLoginRepository = PasswordLoginRepository(serverConfigRepository),
                    serverUrl = serverConfigRepository.currentBaseUrl(),
                )
            }
        }
    }
    return viewModel(factory = factory)
}
