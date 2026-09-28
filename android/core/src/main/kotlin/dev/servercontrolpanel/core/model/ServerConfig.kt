package dev.servercontrolpanel.core.model

data class ServerConfig(
    val baseUrl: String,
    val allowInsecureHttp: Boolean = false,
)
