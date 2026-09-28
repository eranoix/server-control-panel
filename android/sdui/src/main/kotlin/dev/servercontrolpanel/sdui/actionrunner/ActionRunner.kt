package dev.servercontrolpanel.sdui.actionrunner

import dev.servercontrolpanel.core.sdui.SduiJson
import dev.servercontrolpanel.core.sdui.SduiValidationError
import dev.servercontrolpanel.data.sdui.SduiActionHttpResult
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

data class Confirmation(val confirmed: Boolean, val typed: String? = null)

const val ConfirmationFieldKey = "_confirmation"

data class ActionInvocation(
    val actionId: String,
    val params: Map<String, String> = emptyMap(),
    val input: JsonObject? = null,
    val confirmation: Confirmation? = null,
    val destructive: Boolean = false,
)

sealed interface ActionOutcome {
    data object Patched : ActionOutcome

    data class Invalidated(val ids: List<String>) : ActionOutcome

    data class ValidationFailed(val fields: Map<String, List<String>>) : ActionOutcome

    data object Gone : ActionOutcome

    data object Stale : ActionOutcome

    data class Failed(val message: String) : ActionOutcome
}

fun interface ActionInvoker {
    suspend fun invoke(actionId: String, requestBody: JsonObject): SduiActionHttpResult
}

class ActionRunner(
    private val invoker: ActionInvoker,
    private val screenState: ScreenState,
) {
    suspend fun run(action: ActionInvocation): ActionOutcome {
        if (action.destructive && action.confirmation?.confirmed != true) {
            return ActionOutcome.Failed(
                "Destructive action \"${action.actionId}\" dispatched without confirmation.",
            )
        }

        val requestBody = buildJsonObject {
            put(
                "params",
                buildJsonObject { action.params.forEach { (key, value) -> put(key, value) } },
            )
            action.input?.let { put("input", it) }
            action.confirmation?.let { confirmation ->
                put(
                    "confirmation",
                    buildJsonObject {
                        put("confirmed", confirmation.confirmed)
                        confirmation.typed?.let { put("typed", it) }
                    },
                )
            }
        }

        return when (val result = invoker.invoke(action.actionId, requestBody)) {
            is SduiActionHttpResult.Success -> mapSuccess(result.body)
            is SduiActionHttpResult.ValidationFailed -> mapValidationFailed(result.rawBody)
            is SduiActionHttpResult.NotFound -> ActionOutcome.Gone
            is SduiActionHttpResult.Stale -> {
                screenState.refetchScreen()
                ActionOutcome.Stale
            }
            is SduiActionHttpResult.Error -> ActionOutcome.Failed(result.reason)
        }
    }

    private suspend fun mapSuccess(body: JsonObject): ActionOutcome {
        val patch = body["patch"] as? JsonObject
        val invalidate = (body["invalidate"] as? JsonArray)
            ?.mapNotNull { (it as? JsonPrimitive)?.content }

        return when {
            patch != null && invalidate == null -> {
                screenState.applyPatch(patch)
                ActionOutcome.Patched
            }
            invalidate != null && patch == null -> {
                screenState.invalidate(invalidate)
                ActionOutcome.Invalidated(invalidate)
            }
            else -> ActionOutcome.Failed(
                "Malformed action response: expected exactly one of patch/invalidate.",
            )
        }
    }

    private fun mapValidationFailed(rawBody: String): ActionOutcome = try {
        val decoded = SduiJson.decodeFromString(SduiValidationError.serializer(), rawBody)
        ActionOutcome.ValidationFailed(decoded.fields)
    } catch (e: SerializationException) {
        ActionOutcome.Failed("Unexpected validation error body.")
    }
}
