package com.vpsmanager.data.auth

import android.content.Context
import com.vpsmanager.data.config.ServerConfigRepository

/**
 * The only passkey-ceremony type a feature module may reference directly.
 * [PasskeyClient] extends the generated `AuthApi` from
 * `:data:mobile-api-client`, an `implementation`-only dependency of `:data`
 * — referencing [PasskeyClient] itself from outside `:data` would require
 * that module to resolve `AuthApi`, which is not on its compile classpath.
 * Same boundary as [PairingRepository]/[com.vpsmanager.data.sdui.SduiDataRepository]:
 * every public member here trades in plain domain types only
 * ([RegistrationResult], [LoginResult]), never the generated client.
 *
 * [serverConfigRepository] is threaded straight through to [PasskeyClient] —
 * the server this device talks to is always taken from the single
 * persisted config, never guessed at.
 */
/**
 * The slice of [PasskeyRepository] the login screen depends on — same
 * convention as [com.vpsmanager.data.session.SessionSource]: anything outside
 * `:data` tests against a fake without touching Credential Manager or the
 * generated client.
 */
interface PasskeyLoginSource {
    suspend fun login(context: Context): LoginResult
}

class PasskeyRepository(serverConfigRepository: ServerConfigRepository) : PasskeyLoginSource {
    private val client = PasskeyClient(serverConfigRepository)

    /** See [PasskeyClient.register]. */
    suspend fun register(context: Context, regToken: String, label: String? = null): RegistrationResult =
        client.register(context, regToken, label)

    /** See [PasskeyClient.login]. */
    override suspend fun login(context: Context): LoginResult = client.login(context)
}
