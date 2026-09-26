package com.vpsmanager.data.auth

/**
 * The only pairing-flow type a feature module may reference directly.
 * [PairingClient] extends the generated `AuthApi` from
 * `:data:mobile-api-client`, an `implementation`-only dependency of `:data`
 * — referencing [PairingClient] itself from outside `:data` would require
 * that module to resolve `AuthApi`, which is not on its compile classpath.
 * This repository is the same boundary [com.vpsmanager.data.sdui.SduiDataRepository]
 * already draws around `SduiDataClient`: everything below it is private to
 * `:data`, and every public member here trades in plain domain types only
 * ([PairingResult], [String]).
 */
class PairingRepository {
    private val client = PairingClient()

    /** See [PairingClient.parsePairingPayload]. */
    fun parsePairingPayload(qrPayload: String): PairingPayload? = client.parsePairingPayload(qrPayload)

    /** See [PairingClient.consume]. */
    suspend fun consume(ticket: String): PairingResult = client.consume(ticket)
}
