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

    private val corpoCodigoRecusado =
        """{"title":"Unauthorized","status":401,"detail":"código 2FA inválido"}"""
    private val corpoCredencialRecusada =
        """{"title":"Unauthorized","status":401,"detail":"invalid credentials"}"""

    @Test
    fun `401 do segundo fator nao acusa a senha`() {
        val resultado = traduzFalhaDeLogin(401, corpoCodigoRecusado, enviouCodigo = true)

        assertTrue("esperava InvalidCode, veio $resultado", resultado is PasswordLoginResult.InvalidCode)
        val motivo = (resultado as PasswordLoginResult.InvalidCode).reason
        assertTrue("a mensagem nao pode falar em senha: $motivo", !motivo.contains("password", ignoreCase = true))
        assertTrue("a mensagem deve dizer que o codigo expira: $motivo", motivo.contains("30 seconds"))
    }

    @Test
    fun `401 de credencial continua acusando usuario e senha`() {
        val resultado = traduzFalhaDeLogin(401, corpoCredencialRecusada, enviouCodigo = false)

        assertEquals(PasswordLoginResult.Failed("Invalid username or password."), resultado)
    }

    /**
     * The case the context criterion alone would get wrong: the operator
     * reaches the code step and EDITS the password, leaving it wrong. The
     * server answers "invalid credentials", and the screen has to go back to
     * talking about the password — even though a code was sent along with it.
     */
    @Test
    fun `senha editada na etapa do codigo volta a acusar a senha, nao o codigo`() {
        val resultado = traduzFalhaDeLogin(401, corpoCredencialRecusada, enviouCodigo = true)

        assertEquals(PasswordLoginResult.Failed("Invalid username or password."), resultado)
    }

    /**
     * With no body (a proxy that swallows it, the generator changing shape) the
     * fallback criterion is the context: we sent a code, so the 401 is about
     * the code. It degrades to a good guess, never to the false accusation.
     */
    @Test
    fun `sem corpo de erro, ter mandado codigo decide que foi o codigo`() {
        assertTrue(traduzFalhaDeLogin(401, null, enviouCodigo = true) is PasswordLoginResult.InvalidCode)
        assertTrue(traduzFalhaDeLogin(401, "", enviouCodigo = true) is PasswordLoginResult.InvalidCode)
    }

    @Test
    fun `sem corpo e sem codigo enviado, o 401 e de credencial`() {
        assertEquals(
            PasswordLoginResult.Failed("Invalid username or password."),
            traduzFalhaDeLogin(401, null, enviouCodigo = false),
        )
    }

    /**
     * 423 is the lockout by attempts. The message has to say that a wrong CODE
     * counts too, otherwise the operator does not connect one thing to the
     * other — which is exactly how he got here.
     */
    @Test
    fun `423 explica que codigo errado tambem conta para o bloqueio`() {
        val resultado = traduzFalhaDeLogin(423, null, enviouCodigo = true)

        val motivo = (resultado as PasswordLoginResult.Failed).reason
        assertTrue("deve mencionar bloqueio: $motivo", motivo.contains("locked", ignoreCase = true))
        assertTrue("deve ligar ao codigo: $motivo", motivo.contains("code", ignoreCase = true))
    }

    @Test
    fun `429 pede espera, e um status desconhecido nao ecoa o corpo cru`() {
        assertEquals(
            PasswordLoginResult.Failed("Too many attempts. Wait a moment and try again."),
            traduzFalhaDeLogin(429, null, enviouCodigo = false),
        )

        val desconhecido = traduzFalhaDeLogin(418, """{"detail":"segredo do servidor"}""", enviouCodigo = false)
        val motivo = (desconhecido as PasswordLoginResult.Failed).reason
        assertTrue("nao pode ecoar o corpo: $motivo", !motivo.contains("segredo"))
        assertTrue(motivo.contains("418"))
    }
}
