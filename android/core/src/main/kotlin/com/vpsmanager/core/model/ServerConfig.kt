package com.vpsmanager.core.model

/**
 * The single persisted configuration of which server this app talks to.
 * [baseUrl] is always the validated, normalized origin the app was paired
 * or manually configured against (e.g. `https://vpsm.example.com` — no
 * trailing slash, no path), never a per-repository default or a
 * build-time constant. [allowInsecureHttp] exists only for the one
 * deliberate development exception (a local dev server reachable solely
 * over plain HTTP); a real pairing flow always yields an `https` host.
 */
data class ServerConfig(
    val baseUrl: String,
    val allowInsecureHttp: Boolean = false,
)
