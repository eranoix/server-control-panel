package dev.servercontrolpanel.data.auth

import android.content.Context
import dev.servercontrolpanel.data.config.ServerConfigRepository

interface PasskeyLoginSource {
    suspend fun login(context: Context): LoginResult
}

class PasskeyRepository(serverConfigRepository: ServerConfigRepository) : PasskeyLoginSource {
    private val client = PasskeyClient(serverConfigRepository)

    suspend fun register(context: Context, regToken: String, label: String? = null): RegistrationResult =
        client.register(context, regToken, label)

    override suspend fun login(context: Context): LoginResult = client.login(context)
}
