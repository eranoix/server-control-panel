package dev.servercontrolpanel.core.sdui

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

@Serializable
data class SduiActionDescriptor(
    @SerialName("action_id") val actionId: String,
    val endpoint: String,
    val method: String,
    val permission: String,
    @SerialName("body_template") val bodyTemplate: JsonElement? = null,
    val destructive: Boolean = false,
    @SerialName("require_typed_confirmation") val requireTypedConfirmation: String? = null,
)

@Serializable
data class SduiValidationError(
    val error: String,
    val fields: Map<String, List<String>>,
)
