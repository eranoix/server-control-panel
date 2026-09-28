package dev.servercontrolpanel.data.auth

class PairingRepository {
    private val client = PairingClient()

    fun parsePairingPayload(qrPayload: String): PairingPayload? = client.parsePairingPayload(qrPayload)

    suspend fun consume(ticket: String): PairingResult = client.consume(ticket)
}
