package dev.servercontrolpanel.data.terminal

import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient

private const val UNCONFIGURED_WS_BASE_URL = "ws://unconfigured.invalid"

fun defaultTerminalWsBaseUrl(): String {
    val httpBase: String = System.getProperty(ApiClient.BASE_URL_KEY) ?: return UNCONFIGURED_WS_BASE_URL
    val wsScheme = httpBase
        .replaceFirst(Regex("^https"), "wss")
        .replaceFirst(Regex("^http"), "ws")
    return wsScheme.removeSuffix("/api/mobile/v1").removeSuffix("/")
}
