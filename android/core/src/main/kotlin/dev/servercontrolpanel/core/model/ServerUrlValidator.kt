package dev.servercontrolpanel.core.model

import java.net.URI

sealed interface ServerUrlValidation {
    data class Valid(val normalized: String) : ServerUrlValidation
    data class Invalid(val reason: String) : ServerUrlValidation
}

fun validateServerUrl(rawUrl: String, allowInsecureHttp: Boolean = false): ServerUrlValidation {
    val trimmed = rawUrl.trim()
    if (trimmed.isEmpty()) {
        return ServerUrlValidation.Invalid("Empty address.")
    }
    val uri = try {
        URI(trimmed)
    } catch (e: Exception) {
        return ServerUrlValidation.Invalid("Invalid address.")
    }
    val scheme = uri.scheme?.lowercase()
    val host = uri.host
    if (scheme == null || host.isNullOrBlank()) {
        return ServerUrlValidation.Invalid(
            "Enter a full address, with https:// and the server's domain.",
        )
    }
    when (scheme) {
        "https" -> Unit
        "http" -> if (!allowInsecureHttp) {
            return ServerUrlValidation.Invalid(
                "This server needs https:// (http:// is only allowed for local development).",
            )
        }
        else -> return ServerUrlValidation.Invalid("Unsupported scheme: $scheme")
    }
    val normalized = buildString {
        append(scheme)
        append("://")
        append(host)
        if (uri.port != -1) {
            append(":")
            append(uri.port)
        }
    }
    return ServerUrlValidation.Valid(normalized)
}
