package dev.servercontrolpanel.data.config

import dev.servercontrolpanel.core.model.ServerConfig
import dev.servercontrolpanel.core.model.ServerUrlValidation
import dev.servercontrolpanel.core.model.validateServerUrl
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient

/** Outcome of [ServerConfigRepository.configure]. */
sealed interface ConfigureServerResult {
    data class Applied(val config: ServerConfig) : ConfigureServerResult
    data class Rejected(val reason: String) : ConfigureServerResult

    /**
     * The app is already configured for a DIFFERENT host than [attemptedBaseUrl]
     * and `allowRepoint` was not set. An untrusted QR code or deep link must never
     * silently repoint a paired app, which would send future credentials to a
     * stranger's server; only a user-confirmed "change server" action may pass
     * `allowRepoint = true`.
     */
    data class RepointBlocked(val currentBaseUrl: String, val attemptedBaseUrl: String) : ConfigureServerResult
}

/**
 * The single source of truth for which server this app talks to, persisted by
 * [ServerConfigStore] (see [EncryptedServerConfigStore]). Nothing reads
 * `System.getProperty(ApiClient.BASE_URL_KEY)` directly except through
 * [publishLegacyBasePathSeam], the bridge for call sites not yet using
 * constructor injection.
 */
class ServerConfigRepository(private val store: ServerConfigStore) {

    @Volatile
    private var cached: ServerConfig? = store.load()

    /** Null until the app has been configured (paired or manually set up) at least once. */
    fun currentConfig(): ServerConfig? = cached

    /**
     * The absolute origin (e.g. `https://panel.example.com`), or null if never
     * configured. Never guesses `localhost`; callers decide how to handle null.
     */
    fun currentBaseUrl(): String? = cached?.baseUrl

    /**
     * Validates and persists [rawUrl] as the server this app talks to. See
     * [ConfigureServerResult.RepointBlocked] for why an already-configured
     * different host is refused unless [allowRepoint] is explicitly true.
     */
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

    /**
     * Mirrors the current [ServerConfig] into [ApiClient.BASE_URL_KEY], the seam
     * that non-migrated call sites still read (e.g. `WhatsappApi.defaultBasePath`).
     * Only sets a validated absolute URL and never a default, so early readers fail
     * loudly instead of hitting `localhost`. Call at startup and after each
     * successful [configure].
     */
    fun publishLegacyBasePathSeam() {
        val baseUrl = cached?.baseUrl ?: return
        System.setProperty(ApiClient.BASE_URL_KEY, "$baseUrl/api/mobile/v1")
    }
}
