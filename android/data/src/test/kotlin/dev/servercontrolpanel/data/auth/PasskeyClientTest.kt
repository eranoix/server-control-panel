package dev.servercontrolpanel.data.auth

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

private const val REGISTRATION_CHALLENGE = """
    {
      "publicKey": {
        "rp": {"id": "panel.example.com", "name": "Server Control Panel"},
        "user": {"id": "AQID", "name": "sam", "displayName": "Sam"},
        "challenge": "Y2hhbGxlbmdl",
        "pubKeyCredParams": [{"type": "public-key", "alg": -7}]
      },
      "mediation": "optional"
    }
"""

private const val LOGIN_CHALLENGE = """
    {
      "publicKey": {
        "challenge": "bG9naW4tY2hhbGxlbmdl",
        "rpId": "panel.example.com",
        "userVerification": "preferred"
      },
      "mediation": "optional"
    }
"""

class PasskeyClientTest {

    @Test
    fun `buildCreateRequestJson unwraps the publicKey envelope, not a byte-identical copy`() {
        val requestJson = buildCreateRequestJson(REGISTRATION_CHALLENGE)

        assertTrue(!requestJson.contains("\"mediation\""))
        assertTrue(!requestJson.trimStart().startsWith("{\"publicKey\""))
        assertTrue(requestJson.contains("\"rp\""))
        assertTrue(requestJson.contains("panel.example.com"))
    }

    @Test
    fun `buildGetRequestJson unwraps the publicKey envelope for a login challenge`() {
        val requestJson = buildGetRequestJson(LOGIN_CHALLENGE)

        assertTrue(!requestJson.contains("\"mediation\""))
        assertTrue(requestJson.contains("\"rpId\""))
        assertTrue(requestJson.contains("panel.example.com"))
    }

    @Test
    fun `verifyRpId succeeds when registration rp id matches the configured host`() {
        val result = verifyRpId(REGISTRATION_CHALLENGE, "https://panel.example.com/api/mobile/v1")

        assertTrue(result.isSuccess)
    }

    @Test
    fun `verifyRpId succeeds when login rpId matches the configured host`() {
        val result = verifyRpId(LOGIN_CHALLENGE, "https://panel.example.com/api/mobile/v1")

        assertTrue(result.isSuccess)
    }

    @Test
    fun `verifyRpId fails closed when the rp id does not match the configured host`() {
        val result = verifyRpId(REGISTRATION_CHALLENGE, "https://attacker.example.net/api/mobile/v1")

        assertTrue(result.isFailure)
        val error = result.exceptionOrNull()
        assertTrue(error is RpIdMismatchException)
        error as RpIdMismatchException
        assertEquals("attacker.example.net", error.expected)
        assertEquals("panel.example.com", error.actual)
    }

    @Test
    fun `verifyRpId fails closed when the challenge has no publicKey envelope`() {
        val result = verifyRpId("""{"rp": {"id": "panel.example.com"}}""", "https://panel.example.com")

        assertTrue(result.isFailure)
    }
}
