package dev.servercontrolpanel.data.whatsapp

import dev.servercontrolpanel.mobileapiclient.api.WhatsappApi
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

fun resolveWhatsAppWsUrl(basePath: String): String? {
    val httpUrl = basePath.toHttpUrlOrNull() ?: return null
    val hostAndPath = httpUrl.newBuilder()
        .encodedPath("/ws/whatsapp")
        .encodedQuery(null)
        .build()
        .toString()
    val wsScheme = if (httpUrl.isHttps) "wss" else "ws"
    return hostAndPath.replaceFirst(httpUrl.scheme, wsScheme)
}

fun resolveWhatsAppWsUrl(): String? = resolveWhatsAppWsUrl(WhatsappApi.defaultBasePath)
