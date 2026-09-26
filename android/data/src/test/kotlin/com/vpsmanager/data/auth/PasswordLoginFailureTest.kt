package com.vpsmanager.data.auth

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The 401 from `POST /auth/login` covers TWO accidents that call for opposite
 * reactions — wrong password (go back to the password) and wrong second factor
 * (stay on the code) — and before this work the app answered both with the
 * same sentence, "Invalid user, password or code."
 *
 * At the code step that sentence is a FALSE accusation: the server only asks
 * for a code AFTER accepting the password, so at that point the password is
 * provably right. Whoever read it went back and retyped the password — and
 * since every refused attempt counts towards the server's 5-failure lockout
 * (`auth.NewLockout(5, …)` in `internal/api/api.go`), the path to "account
 * temporarily locked" was a short one. These tests pin the separation.
 */
class PasswordLoginFailureTest {

    private val rejectedCodeBody =
        """{"title":"Unauthorized","status":401,"detail":"código 2FA inválido"}"""
    private val rejectedCredentialBody =
        """{"title":"Unauthorized","status":401,"detail":"invalid credentials"}"""

    @Test
    fun `401 do segundo fator nao acusa a senha`() {
        val result = translateLoginFailure(401, rejectedCodeBody, sentCode = true)

        assertTrue("esperava InvalidCode, veio $result", result is PasswordLoginResult.InvalidCode)
        val reason = (result as PasswordLoginResult.InvalidCode).reason
        assertTrue("a mensagem nao pode falar em senha: $reason", !reason.contains("password", ignoreCase = true))
        assertTrue("a mensagem deve dizer que o codigo expira: $reason", reason.contains("30 seconds"))
    }

    @Test
    fun `401 de credencial continua acusando usuario e senha`() {
        val result = translateLoginFailure(401, rejectedCredentialBody, sentCode = false)

        assertEquals(PasswordLoginResult.Failed("Invalid username or password."), result)
    }

    /**
     * The case the context criterion alone would get wrong: the operator
     * reaches the code step and EDITS the password, leaving it wrong. The
     * server answers "invalid credentials", and the screen has to go back to
     * talking about the password — even though a code was sent along with it.
     */
    @Test
    fun `senha editada na etapa do codigo volta a acusar a senha, nao o codigo`() {
        val result = translateLoginFailure(401, rejectedCredentialBody, sentCode = true)

        assertEquals(PasswordLoginResult.Failed("Invalid username or password."), result)
    }

    /**
     * With no body (a proxy that swallows it, the generator changing shape) the
     * fallback criterion is the context: we sent a code, so the 401 is about
     * the code. It degrades to a good guess, never to the false accusation.
     */
    @Test
    fun `sem corpo de erro, ter mandado codigo decide que foi o codigo`() {
        assertTrue(translateLoginFailure(401, null, sentCode = true) is PasswordLoginResult.InvalidCode)
        assertTrue(translateLoginFailure(401, "", sentCode = true) is PasswordLoginResult.InvalidCode)
    }

    @Test
    fun `sem corpo e sem codigo enviado, o 401 e de credencial`() {
        assertEquals(
            PasswordLoginResult.Failed("Invalid username or password."),
            translateLoginFailure(401, null, sentCode = false),
        )
    }

    /**
     * 423 is the lockout by attempts. The message has to say that a wrong CODE
     * counts too, otherwise the operator does not connect one thing to the
     * other — which is exactly how he got here.
     */
    @Test
    fun `423 explica que codigo errado tambem conta para o bloqueio`() {
        val result = translateLoginFailure(423, null, sentCode = true)

        val reason = (result as PasswordLoginResult.Failed).reason
        assertTrue("deve mencionar bloqueio: $reason", reason.contains("locked", ignoreCase = true))
        assertTrue("deve ligar ao codigo: $reason", reason.contains("code", ignoreCase = true))
    }

    @Test
    fun `429 pede espera, e um status desconhecido nao ecoa o corpo cru`() {
        assertEquals(
            PasswordLoginResult.Failed("Too many attempts. Wait a moment and try again."),
            translateLoginFailure(429, null, sentCode = false),
        )

        val unknown = translateLoginFailure(418, """{"detail":"segredo do servidor"}""", sentCode = false)
        val reason = (unknown as PasswordLoginResult.Failed).reason
        assertTrue("nao pode ecoar o corpo: $reason", !reason.contains("segredo"))
        assertTrue(reason.contains("418"))
    }
}
