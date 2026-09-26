package com.vpsmanager.data.config

import com.vpsmanager.core.model.ServerConfig
import com.vpsmanager.core.model.ServerUrlValidation
import com.vpsmanager.core.model.validateServerUrl
import com.vpsmanager.mobileapiclient.infrastructure.ApiClient

/** Outcome of [ServerConfigRepository.configure]. */
sealed interface ConfigureServerResult {
    data class Applied(val config: ServerConfig) : ConfigureServerResult
    data class Rejected(val reason: String) : ConfigureServerResult

    /**
     * The app is already configured against a DIFFERENT host than
     * [attemptedBaseUrl] and [ServerConfigRepository.configure] was not
     * called with `allowRepoint = true`. An untrusted QR code or deep link
     * must never silently repoint an already-paired app at another
     * server — that would hand a stranger's server every future credential
     * this device sends. Only a deliberate, user-confirmed "trocar
     * servidor" action may pass `allowRepoint = true`.
     */
    data class RepointBlocked(val currentBaseUrl: String, val attemptedBaseUrl: String) : ConfigureServerResult
}

/**
 * The single source of truth for which server this app talks to, backed by
 * [ServerConfigStore] (persisted across restarts — see
 * [EncryptedServerConfigStore]). Repositories that need the base URL take
 * this class through their constructor; nothing reads
 * `System.getProperty(ApiClient.BASE_URL_KEY)` directly anymore except
 * [publishLegacyBasePathSeam] itself, the one deliberate bridge into the
 * generated client's own config knob for call sites not yet migrated to
 * constructor injection.
 */
class ServerConfigRepository(private val store: ServerConfigStore) {

    @Volatile
    private var cached: ServerConfig? = store.load()

    /** Null until the app has been configured (paired or manually set up) at least once. */
    fun currentConfig(): ServerConfig? = cached

    /**
     * The absolute origin (e.g. `https://vpsm.example.com`) this app is
     * configured to talk to, or null if never configured. Callers decide
     * for themselves whether that is a hard failure or a "go to setup"
     * navigation — this never guesses `localhost`.
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
     * Mirrors the current [ServerConfig] into
     * [ApiClient.BASE_URL_KEY] — the generated `*Api.defaultBasePath` seam
     * every call site not yet migrated to constructor injection still
     * reads (e.g. `com.vpsmanager.data.terminal.defaultTerminalWsBaseUrl`,
     * `WhatsappApi.defaultBasePath`). Only ever SETS the property to a
     * real, validated, absolute URL when a config exists; never falls back
     * to a default, so anything reading the property before this ever ran
     * keeps failing loud instead of resolving to `localhost`. Call once at
     * app startup (after loading any persisted config) and again whenever
     * [configure] succeeds.
     */
    fun publishLegacyBasePathSeam() {
        val baseUrl = cached?.baseUrl ?: return
        System.setProperty(ApiClient.BASE_URL_KEY, "$baseUrl/api/mobile/v1")
    }
}
