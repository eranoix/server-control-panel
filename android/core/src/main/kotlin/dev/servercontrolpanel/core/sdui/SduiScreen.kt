package dev.servercontrolpanel.core.sdui

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class SduiEnvelope(
    @SerialName("sdui_version") val sduiVersion: Int,
    val screen: SduiScreen,
)

@Serializable
data class SduiScreen(
    val id: String,
    val title: String,
    val components: List<SduiComponent>,
)
