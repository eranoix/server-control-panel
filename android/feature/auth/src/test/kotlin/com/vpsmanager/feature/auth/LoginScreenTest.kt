package com.vpsmanager.feature.auth

import android.content.Context
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.auth.InMemoryTokenStore
import com.vpsmanager.data.auth.LoginResult
import com.vpsmanager.data.auth.PasskeyError
import com.vpsmanager.data.auth.PasskeyLoginSource
import com.vpsmanager.data.auth.PasswordLoginResult
import com.vpsmanager.data.auth.PasswordLoginSource
import com.vpsmanager.data.auth.RefreshOutcome
import com.vpsmanager.data.auth.SessionManager
import com.vpsmanager.data.auth.SessionRefresher
import com.vpsmanager.data.auth.SessionState
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

private class FakePasskeyLogin(private val result: suspend () -> LoginResult) : PasskeyLoginSource {
    override suspend fun login(context: Context): LoginResult = result()
}

private class FakePasswordLogin(private val result: suspend (String?) -> PasswordLoginResult) : PasswordLoginSource {
    var lastTotpCode: String? = null
    var calls = 0
    override suspend fun login(username: String, password: String, totpCode: String?): PasswordLoginResult {
        calls++
        lastTotpCode = totpCode
        return result(totpCode)
    }
}

private object NeverRefreshes : SessionRefresher {
    override suspend fun refresh(refreshToken: String): RefreshOutcome = RefreshOutcome.Unavailable
}

/**
 * Renders [LoginScreen] — the screen that simply did not exist. Before this
 * work, `PasskeyRepository.login()` was defined and never called by anyone,
 * and a freshly installed device dropped straight into `AppNavHost` with no
 * credential: every screen showed "error 401" and a "Try again" with no
 * future.
 */
@RunWith(RobolectricTestRunner::class)
class LoginScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun sessionManager() = SessionManager(
        tokenStore = InMemoryTokenStore(),
        refresher = NeverRefreshes,
        publishAccessToken = {},
    )

    private fun viewModel(
        session: SessionManager = sessionManager(),
        passkey: PasskeyLoginSource = FakePasskeyLogin { LoginResult.PendingApproval },
        password: PasswordLoginSource = FakePasswordLogin { PasswordLoginResult.Failed("nao usado") },
        serverUrl: String? = "https://vpsmanager.exemplo.test",
    ) = LoginViewModel(session, passkey, password, serverUrl)

    @Test
    fun `a tela oferece os tres caminhos — passkey, senha e pareamento`() {
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = viewModel()) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").assertExists()
        composeRule.onNodeWithText("Sign in with username and password").assertExists()
        composeRule.onNodeWithText("Pair this device (QR code)").assertExists()
    }

    @Test
    fun `mostra o servidor com que o aparelho vai falar`() {
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = viewModel()) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Server: https://vpsmanager.exemplo.test").assertExists()
    }

    @Test
    fun `um login por passkey bem-sucedido estabelece a sessao`() {
        val session = sessionManager()
        val vm = viewModel(
            session = session,
            passkey = FakePasskeyLogin { LoginResult.Success("acc", "ref", 900) },
        )
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").performClick()
        composeRule.waitForIdle()

        assertTrue(session.state.value is SessionState.SignedIn)
        assertEquals("acc", session.currentAccessToken())
    }

    @Test
    fun `pending_approval diz ao operador exatamente onde aprovar — nao e um beco`() {
        val vm = viewModel(passkey = FakePasskeyLogin { LoginResult.PendingApproval })
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Waiting for approval in the panel").assertExists()
        composeRule.onNodeWithText(
            text = "Paired devices (Android app)",
            substring = true,
        ).assertExists()
        // The way out while the approval is outstanding stays on screen.
        composeRule.onNodeWithText("Sign in with username and password").assertExists()
    }

    @Test
    fun `sem passkey no aparelho a mensagem manda parear, nao so tentar de novo`() {
        val vm = viewModel(passkey = FakePasskeyLogin { LoginResult.Failed(PasskeyError.NoPasskeyAvailable) })
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText(
            text = "to register a passkey by QR code",
            substring = true,
        ).assertExists()
    }

    @Test
    fun `o botao de parear leva ao fluxo de QR`() {
        var pediuPareamento = false
        composeRule.setContent {
            LoginScreen(onPairDeviceRequested = { pediuPareamento = true }, viewModel = viewModel())
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Pair this device (QR code)").performClick()
        composeRule.waitForIdle()

        assertTrue(pediuPareamento)
    }

    @Test
    fun `o formulario de senha aparece e loga`() {
        val session = sessionManager()
        val password = FakePasswordLogin { PasswordLoginResult.Success("acc-senha", "ref", 900) }
        val vm = viewModel(session = session, password = password)
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with username and password").performClick()
        composeRule.waitForIdle()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("segredo")
        composeRule.onNodeWithText("Sign in").performClick()
        composeRule.waitForIdle()

        assertEquals("acc-senha", session.currentAccessToken())
    }

    @Test
    fun `totp_required pede o codigo em vez de tratar como erro`() {
        val password = FakePasswordLogin { codigo ->
            if (codigo == null) PasswordLoginResult.TotpRequired else PasswordLoginResult.Success("acc", "ref", 900)
        }
        val session = sessionManager()
        val vm = viewModel(session = session, password = password)
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("segredo")
        vm.submitPassword()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Verification code").assertExists()
        // Second attempt, this time with the code: in.
        vm.onTotpChange("123456")
        vm.submitPassword()
        composeRule.waitForIdle()

        assertEquals("123456", password.lastTotpCode)
        assertEquals("acc", session.currentAccessToken())
    }

    /**
     * The "Sign in" button with the code field EMPTY was a silent nothing: the
     * server receives an empty code, answers `totp_required` again (not an
     * error — see `MobileLogin`, the `mfaCodeRequired` branch), and the screen
     * refreshes into the state it was already in. No message, no change: the
     * operator taps and concludes the app has frozen.
     */
    @Test
    fun `codigo em branco nao vira ida muda ao servidor — a tela diz o que falta`() {
        val password = FakePasswordLogin { codigo ->
            if (codigo == null) PasswordLoginResult.TotpRequired else PasswordLoginResult.Success("acc", "ref", 900)
        }
        val vm = viewModel(password = password)
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("segredo")
        vm.submitPassword()
        composeRule.waitForIdle()
        assertEquals(1, password.calls)

        // Second tap, code field still empty: it does NOT call the server.
        vm.submitPassword()
        composeRule.waitForIdle()

        assertEquals("nao podia ter chamado o servidor de novo", 1, password.calls)
        composeRule.onNodeWithText("Enter the verification code to continue.").assertExists()
    }

    /**
     * A rejected code keeps the operator ON THE CODE STEP and clears only that
     * field. Sending them back to the password (which the old message did) is
     * the shortest route to the server's attempt lockout.
     */
    @Test
    fun `codigo recusado fica na etapa do codigo, limpa o campo e nao acusa a senha`() {
        val vm = viewModel(
            password = FakePasswordLogin { codigo ->
                if (codigo == null) {
                    PasswordLoginResult.TotpRequired
                } else {
                    PasswordLoginResult.InvalidCode("Código inválido ou expirado.")
                }
            },
        )
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("segredo")
        vm.submitPassword()
        composeRule.waitForIdle()

        vm.onTotpChange("000000")
        vm.submitPassword()
        composeRule.waitForIdle()

        val estado = vm.uiState.value
        assertTrue("continua pedindo o codigo", estado.totpRequired)
        assertEquals("o campo do codigo tem que vir limpo", "", estado.totpCode)
        assertEquals("a senha nao se perde", "segredo", estado.password)
        composeRule.onNodeWithText("Código inválido ou expirado.").assertExists()
        // The code field stays on screen for the next attempt.
        composeRule.onNodeWithText("Verification code").assertExists()
    }

    /**
     * `totpRequired` is the server's answer ABOUT AN ACCOUNT. This screen
     * survives a sign-out (the ViewModel belongs to the process), so carrying
     * it over to the next account would show the code field to someone who may
     * not even use 2FA — and would send the old code along on the first
     * attempt.
     */
    @Test
    fun `entrar zera a etapa do codigo, e trocar de usuario tambem`() {
        val session = sessionManager()
        val vm = viewModel(
            session = session,
            password = FakePasswordLogin { codigo ->
                if (codigo == null) PasswordLoginResult.TotpRequired else PasswordLoginResult.Success("acc", "ref", 900)
            },
        )
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("segredo")
        vm.submitPassword()
        composeRule.waitForIdle()
        assertTrue(vm.uiState.value.totpRequired)

        vm.onTotpChange("123456")
        vm.submitPassword()
        composeRule.waitForIdle()

        assertEquals("acc", session.currentAccessToken())
        assertTrue("a etapa do codigo nao pode sobreviver ao login", !vm.uiState.value.totpRequired)

        // And switching account drops the code step regardless.
        // (The password is cleared on success, so fill it in again before retrying.)
        vm.onPasswordChange("segredo")
        vm.submitPassword() // asks for the code again for "sam"
        composeRule.waitForIdle()
        assertTrue(vm.uiState.value.totpRequired)
        vm.onUsernameChange("jordan")
        assertTrue("trocar de usuario derruba a etapa do codigo", !vm.uiState.value.totpRequired)
        assertEquals("", vm.uiState.value.totpCode)
    }

    @Test
    fun `credenciais erradas mostram o motivo e a tela continua utilizavel`() {
        val vm = viewModel(password = FakePasswordLogin { PasswordLoginResult.Failed("Usuário, senha ou código inválido.") })
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("errada")
        vm.submitPassword()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Usuário, senha ou código inválido.").assertExists()
        composeRule.onNodeWithText("Sign in").assertExists()
    }

    @Test
    fun `uma guarda so em memoria avisa que a sessao nao sera lembrada`() {
        // InMemoryTokenStore.isPersistent is false — the same state
        // KeystoreTokenStore ends up in when the device's Keystore fails. The
        // operator has to know BEFORE they start wondering.
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = viewModel()) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The session will not be remembered").assertExists()
    }

    @Test
    fun `enquanto a cerimonia roda a tela mostra progresso, nao fica muda`() {
        val vm = viewModel(passkey = FakePasskeyLogin { awaitCancellation() })
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Authenticating…").assertExists()
    }
}
