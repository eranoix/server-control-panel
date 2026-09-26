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

/**
 * The confirmation payload the server demands for a destructive action
 * (`sdui.Confirmation` in plan 07-04). [typed] is compared byte-for-byte
 * against the component's `require_typed_confirmation` on the server —
 * this client never trims or case-folds it either (see
 * `ConfirmDestructiveComponent`), so the two sides never disagree about
 * what counts as confirmed.
 */
data class Confirmation(val confirmed: Boolean, val typed: String? = null)

/**
 * Mirrors `sdui.ConfirmationFieldKey` on the server (`internal/mobilebff/sdui/validation.go`):
 * the reserved 422 field key that refers to the confirmation dialog, never to
 * a real form input. No [dev.servercontrolpanel.core.sdui.SduiFormField] may declare
 * this key — `bindErrors` (plan 07-06 Task 2) routes a message under this key
 * to the confirmation slot instead of any input.
 */
const val ConfirmationFieldKey = "_confirmation"

/**
 * One dispatch of a screen action.
 *
 * [destructive] is set by the caller from a [confirmationFor] lookup
 * against the current screen — never inferred from [actionId]'s text (no
 * verb heuristic exists anywhere in this module). This flag exists in place
 * of a fuller action descriptor because no such descriptor is ever
 * delivered to the client: `sdui.ActionsFor` on the server side is unused
 * outside its own tests, and no envelope field carries one. The
 * server-declared `confirm_destructive` component is the only signal a
 * client ever has that an action is destructive.
 */
data class ActionInvocation(
    val actionId: String,
    val params: Map<String, String> = emptyMap(),
    val input: JsonObject? = null,
    val confirmation: Confirmation? = null,
    val destructive: Boolean = false,
)

/**
 * What happened to one [ActionInvocation]. Every branch of
 * `POST /api/mobile/v1/actions/{action_id}` in plan 07-06's `<interfaces>`
 * contract maps to exactly one of these — no branch is left to a caller's
 * guess.
 */
sealed interface ActionOutcome {
    /** 200 `{"patch": ...}` — already applied to the runner's [ScreenState]. */
    data object Patched : ActionOutcome

    /** 200 `{"invalidate": [...]}` — those components have already been refetched. */
    data class Invalidated(val ids: List<String>) : ActionOutcome

    /**
     * 422 — per-field messages, keyed exactly as the server sent them
     * (including `_confirmation` when present). Routing a message to the
     * right UI slot — an input, the confirmation dialog, or a form-level
     * bucket — is `bindErrors`'s job (plan 07-06 Task 2), not this
     * runner's.
     */
    data class ValidationFailed(val fields: Map<String, List<String>>) : ActionOutcome

    /**
     * 404 — the action id is unknown or not permitted for this viewer;
     * those two cases are deliberately indistinguishable. The documented
     * caller behaviour is to refetch the screen and tell the user the
     * action is no longer available — never a crash, never a silent no-op.
     */
    data object Gone : ActionOutcome

    /**
     * 403 — structurally unreachable unless the server's descriptor and
     * this viewer's actual permissions have diverged, which is a server
     * bug, not a response a client should ever have to negotiate. Reaching
     * it means resyncing, not arguing: by the time a caller observes this
     * outcome, the runner has already refetched the whole screen.
     */
    data object Stale : ActionOutcome

    data class Failed(val message: String) : ActionOutcome
}

/**
 * Fakeable seam over `dev.servercontrolpanel.data.sdui.SduiActionRepository`: this
 * mirrors [ComponentDataFetcher]'s pattern so tests can supply a counting
 * fake without a mocking library. Production passes
 * `SduiActionRepository::invoke` directly — a method reference already
 * satisfies a SAM interface.
 */
fun interface ActionInvoker {
    suspend fun invoke(actionId: String, requestBody: JsonObject): SduiActionHttpResult
}

/**
 * The single mutation path for every SDUI screen. It never
 * builds a URL from data — the action id is the whole address, resolved by
 * [ActionInvoker] (backed in production by
 * `dev.servercontrolpanel.data.sdui.SduiActionRepository`) into
 * `/api/mobile/v1/actions/{action_id}`.
 */
class ActionRunner(
    private val invoker: ActionInvoker,
    private val screenState: ScreenState,
) {
    suspend fun run(action: ActionInvocation): ActionOutcome {
        if (action.destructive && action.confirmation?.confirmed != true) {
            // Defence in depth, not the security boundary: the server
            // refuses an unconfirmed destructive action regardless (plan
            // 07-04). A client able to dispatch one anyway is a client bug
            // worth failing loudly here, not a gap this check alone closes.
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
                // The server emitted a descriptor it will no longer honour
                // for this viewer -- resync rather than argue.
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
