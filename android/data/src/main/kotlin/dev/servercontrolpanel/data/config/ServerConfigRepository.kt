package dev.servercontrolpanel.data.config

import dev.servercontrolpanel.core.model.ServerConfig
import dev.servercontrolpanel.core.model.ServerUrlValidation
import dev.servercontrolpanel.core.model.validateServerUrl
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient

sealed interface ConfigureServerResult {
    data class Applied(val config: ServerConfig) : ConfigureServerResult
    data class Rejected(val reason: String) : ConfigureServerResult

    data class RepointBlocked(val currentBaseUrl: String, val attemptedBaseUrl: String) : ConfigureServerResult
}

class ServerConfigRepository(private val store: ServerConfigStore) {

    @Volatile
    private var cached: ServerConfig? = store.load()

    fun currentConfig(): ServerConfig? = cached

    fun currentBaseUrl(): String? = cached?.baseUrl

    fun configure(
        rawUrl: String,
        allowInsecureHttp: Boolean = false,
        allowRepoint: Boolean = false,
    ): ConfigureServerResult {
        val validation = validateServerUrl(rawUrl, allowInsecureHttp)
        val normalized = when (validation) {
            is ServerUrlValidation.Invalid -> return ConfigureServerResult.Rejected(validation.reason)
            is ServerUrlValidation.Valid -> validation.normalized
        }
        val existing = cached
        if (existing != null && existing.baseUrl != normalized && !allowRepoint) {
            return ConfigureServerResult.RepointBlocked(
                currentBaseUrl = existing.baseUrl,
                attemptedBaseUrl = normalized,
            )
        }
        val config = ServerConfig(baseUrl = normalized, allowInsecureHttp = allowInsecureHttp)
        store.save(config)
        cached = config
        publishLegacyBasePathSeam()
        return ConfigureServerResult.Applied(config)
    }

    fun publishLegacyBasePathSeam() {
        val baseUrl = cached?.baseUrl ?: return
        System.setProperty(ApiClient.BASE_URL_KEY, "$baseUrl/api/mobile/v1")
    }
}
