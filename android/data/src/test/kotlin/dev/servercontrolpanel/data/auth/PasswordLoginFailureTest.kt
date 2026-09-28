package dev.servercontrolpanel.data.auth

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PasswordLoginFailureTest {

    private val rejectedCodeBody =
        """{"title":"Unauthorized","status":401,"detail":"invalid 2FA code"}"""
    private val rejectedCredentialBody =
        """{"title":"Unauthorized","status":401,"detail":"invalid credentials"}"""

    @Test
    fun `a second factor 401 does not blame the password`() {
        val result = translateLoginFailure(401, rejectedCodeBody, sentCode = true)

        assertTrue("expected InvalidCode, got $result", result is PasswordLoginResult.InvalidCode)
        val reason = (result as PasswordLoginResult.InvalidCode).reason
        assertTrue("the message must not mention the password: $reason", !reason.contains("password", ignoreCase = true))
        assertTrue("the message must say the code expires: $reason", reason.contains("30 seconds"))
    }

    @Test
    fun `a credential 401 still blames username and password`() {
        val result = translateLoginFailure(401, rejectedCredentialBody, sentCode = false)

        assertEquals(PasswordLoginResult.Failed("Invalid username or password."), result)
    }

    @Test
    fun `a password edited at the code step blames the password, not the code`() {
        val result = translateLoginFailure(401, rejectedCredentialBody, sentCode = true)

        assertEquals(PasswordLoginResult.Failed("Invalid username or password."), result)
    }

    @Test
    fun `without an error body, having sent a code means the code was wrong`() {
        assertTrue(translateLoginFailure(401, null, sentCode = true) is PasswordLoginResult.InvalidCode)
        assertTrue(translateLoginFailure(401, "", sentCode = true) is PasswordLoginResult.InvalidCode)
    }

    @Test
    fun `without a body and without a code, the 401 is about credentials`() {
        assertEquals(
            PasswordLoginResult.Failed("Invalid username or password."),
            translateLoginFailure(401, null, sentCode = false),
        )
    }

    @Test
    fun `a 423 explains that wrong codes also count towards the lockout`() {
        val result = translateLoginFailure(423, null, sentCode = true)

        val reason = (result as PasswordLoginResult.Failed).reason
        assertTrue("must mention the lockout: $reason", reason.contains("locked", ignoreCase = true))
        assertTrue("must relate it to the code: $reason", reason.contains("code", ignoreCase = true))
    }

    @Test
    fun `a 429 asks to wait, and an unknown status does not echo the raw body`() {
        assertEquals(
            PasswordLoginResult.Failed("Too many attempts. Wait a moment and try again."),
            translateLoginFailure(429, null, sentCode = false),
        )

        val unknown = translateLoginFailure(418, """{"detail":"server secret"}""", sentCode = false)
        val reason = (unknown as PasswordLoginResult.Failed).reason
        assertTrue("must not echo the body: $reason", !reason.contains("secret"))
        assertTrue(reason.contains("418"))
    }
}
