package com.vpsmanager.data.terminal

import com.vpsmanager.mobileapiclient.infrastructure.ApiClient

/**
 * A `/ws/shell` origin that can never resolve to a real host, deliberately
 * shaped like a URL so callers building a request from it fail with a
 * connection error rather than an exception thrown mid-construction.
 * `.invalid` is the RFC 2606 TLD reserved for exactly this — "this is never
 * a real domain" — never a `localhost` guess a device might actually have
 * something listening on.
 */
private const val UNCONFIGURED_WS_BASE_URL = "ws://unconfigured.invalid"

/**
 * Derives the `/ws/shell` origin `TerminalSocketClient` needs from the exact
 * same seam the generated HTTP client already reads for its own base path
 * (`MobileApi.defaultBasePath`'s [ApiClient.BASE_URL_KEY] system property).
 * That property is set exactly once real configuration exists —
 * `com.vpsmanager.data.config.ServerConfigRepository.publishLegacyBasePathSeam`
 * — so both REST calls and this WebSocket origin always resolve from the
 * same configured host, never two different servers by accident. Returns
 * [UNCONFIGURED_WS_BASE_URL] (never `localhost`) when the app has not been
 * configured yet, so an unconfigured device fails to connect loudly instead
 * of silently trying — and possibly succeeding against — whatever happens
 * to be listening on the device's own loopback interface. Lives in `:data`
 * (never `:feature-terminal`) because it is the one module allowed to
 * reference [ApiClient] directly.
 */
fun defaultTerminalWsBaseUrl(): String {
    val httpBase: String = System.getProperty(ApiClient.BASE_URL_KEY) ?: return UNCONFIGURED_WS_BASE_URL
    val wsScheme = httpBase
        .replaceFirst(Regex("^https"), "wss")
        .replaceFirst(Regex("^http"), "ws")
    return wsScheme.removeSuffix("/api/mobile/v1").removeSuffix("/")
}
