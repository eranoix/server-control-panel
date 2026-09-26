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
 * Renders [LoginScreen] with fake passkey and password sources.
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
        password: PasswordLoginSource = FakePasswordLogin { PasswordLoginResult.Failed("unused") },
        serverUrl: String? = "https://vpsmanager.example.test",
    ) = LoginViewModel(session, passkey, password, serverUrl)

    @Test
    fun `the screen offers three paths, passkey, password and pairing`() {
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = viewModel()) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").assertExists()
        composeRule.onNodeWithText("Sign in with username and password").assertExists()
        composeRule.onNodeWithText("Pair this device (QR code)").assertExists()
    }

    @Test
    fun `shows the server the device will talk to`() {
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = viewModel()) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Server: https://vpsmanager.example.test").assertExists()
    }

    @Test
    fun `a successful passkey login establishes the session`() {
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
    fun `pending_approval tells the operator exactly where to approve`() {
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
    fun `with no passkey on the device the message says to pair, not just retry`() {
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
    fun `the pair button opens the QR flow`() {
        var pairingRequested = false
        composeRule.setContent {
            LoginScreen(onPairDeviceRequested = { pairingRequested = true }, viewModel = viewModel())
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Pair this device (QR code)").performClick()
        composeRule.waitForIdle()

        assertTrue(pairingRequested)
    }

    @Test
    fun `the password form appears and signs in`() {
        val session = sessionManager()
        val password = FakePasswordLogin { PasswordLoginResult.Success("acc-password", "ref", 900) }
        val vm = viewModel(session = session, password = password)
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with username and password").performClick()
        composeRule.waitForIdle()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("secret")
        composeRule.onNodeWithText("Sign in").performClick()
        composeRule.waitForIdle()

        assertEquals("acc-password", session.currentAccessToken())
    }

    @Test
    fun `totp_required asks for the code instead of treating it as an error`() {
        val password = FakePasswordLogin { code ->
            if (code == null) PasswordLoginResult.TotpRequired else PasswordLoginResult.Success("acc", "ref", 900)
        }
        val session = sessionManager()
        val vm = viewModel(session = session, password = password)
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("secret")
        vm.submitPassword()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Verification code").assertExists()
        // Second attempt with the code succeeds.
        vm.onTotpChange("123456")
        vm.submitPassword()
        composeRule.waitForIdle()

        assertEquals("123456", password.lastTotpCode)
        assertEquals("acc", session.currentAccessToken())
    }

    /**
     * Submitting an empty code would just get `totp_required` again with no
     * visible change, so the screen must say what is missing instead.
     */
    @Test
    fun `a blank code does not hit the server and the screen says what is missing`() {
        val password = FakePasswordLogin { code ->
            if (code == null) PasswordLoginResult.TotpRequired else PasswordLoginResult.Success("acc", "ref", 900)
        }
        val vm = viewModel(password = password)
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("secret")
        vm.submitPassword()
        composeRule.waitForIdle()
        assertEquals(1, password.calls)

        // Second tap with the code still empty must not call the server.
        vm.submitPassword()
        composeRule.waitForIdle()

        assertEquals("must not have called the server again", 1, password.calls)
        composeRule.onNodeWithText("Enter the verification code to continue.").assertExists()
    }

    /**
     * A rejected code stays on the code step and clears only that field;
     * sending the user back to the password invites the server's lockout.
     */
    @Test
    fun `a rejected code stays on the code step, clears the field and keeps the password`() {
        val vm = viewModel(
            password = FakePasswordLogin { code ->
                if (code == null) {
                    PasswordLoginResult.TotpRequired
                } else {
                    PasswordLoginResult.InvalidCode("Invalid or expired code.")
                }
            },
        )
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("secret")
        vm.submitPassword()
        composeRule.waitForIdle()

        vm.onTotpChange("000000")
        vm.submitPassword()
        composeRule.waitForIdle()

        val state = vm.uiState.value
        assertTrue("still asking for the code", state.totpRequired)
        assertEquals("the code field must be cleared", "", state.totpCode)
        assertEquals("the password is kept", "secret", state.password)
        composeRule.onNodeWithText("Invalid or expired code.").assertExists()
        composeRule.onNodeWithText("Verification code").assertExists()
    }

    /**
     * `totpRequired` belongs to one account. The ViewModel outlives sign-out, so
     * it must reset on login and on a username change.
     */
    @Test
    fun `signing in resets the code step, and so does changing user`() {
        val session = sessionManager()
        val vm = viewModel(
            session = session,
            password = FakePasswordLogin { code ->
                if (code == null) PasswordLoginResult.TotpRequired else PasswordLoginResult.Success("acc", "ref", 900)
            },
        )
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("secret")
        vm.submitPassword()
        composeRule.waitForIdle()
        assertTrue(vm.uiState.value.totpRequired)

        vm.onTotpChange("123456")
        vm.submitPassword()
        composeRule.waitForIdle()

        assertEquals("acc", session.currentAccessToken())
        assertTrue("the code step must not survive sign-in", !vm.uiState.value.totpRequired)

        // Changing user also drops the code step. The password is cleared on
        // success, so fill it in again first.
        vm.onPasswordChange("secret")
        vm.submitPassword() // asks for the code again for "sam"
        composeRule.waitForIdle()
        assertTrue(vm.uiState.value.totpRequired)
        vm.onUsernameChange("jordan")
        assertTrue("changing user drops the code step", !vm.uiState.value.totpRequired)
        assertEquals("", vm.uiState.value.totpCode)
    }

    @Test
    fun `wrong credentials show the reason and the screen stays usable`() {
        val vm = viewModel(password = FakePasswordLogin { PasswordLoginResult.Failed("Invalid username, password or code.") })
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        vm.showPasswordForm()
        vm.onUsernameChange("sam")
        vm.onPasswordChange("wrong")
        vm.submitPassword()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Invalid username, password or code.").assertExists()
        composeRule.onNodeWithText("Sign in").assertExists()
    }

    @Test
    fun `an in-memory token store warns that the session will not be remembered`() {
        // Same state KeystoreTokenStore falls into when the device Keystore fails.
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = viewModel()) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The session will not be remembered").assertExists()
    }

    @Test
    fun `while the ceremony runs the screen shows progress`() {
        val vm = viewModel(passkey = FakePasskeyLogin { awaitCancellation() })
        composeRule.setContent { LoginScreen(onPairDeviceRequested = {}, viewModel = vm) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Sign in with passkey").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Authenticating…").assertExists()
    }
}
