package com.vpsmanager.core.sdui

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * The server-resolved shape of an action referenced by an [SduiActionRef]
 * (`row_actions`/`submit_action`/`confirm_destructive.action_id`). The client
 * never constructs a request URL from data — it always resolves the action
 * id to one of these descriptors first.
 */
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

/**
 * The RFC7807-flavored 422 body a `form` submit maps onto its own field
 * `key`s to render inline errors. Parsed from
 * `contracts/sdui/fixtures/validation-error.json` in plan 07-05's tests.
 */
@Serializable
data class SduiValidationError(
    val error: String,
    val fields: Map<String, List<String>>,
)
