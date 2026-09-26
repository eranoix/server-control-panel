package com.vpsmanager.core.sdui

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Top-level SDUI response envelope: `{sdui_version, screen: {...}}`.
 *
 * `sduiVersion` is bumped only on a breaking change to the envelope itself —
 * never when a new component type is added, since [SduiComponent.Unknown] is
 * the mechanism that makes new component types non-breaking.
 */
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
