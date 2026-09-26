package dev.servercontrolpanel.sdui.registry

import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.sdui.action.ActionComponent
import dev.servercontrolpanel.sdui.actionrunner.ActionOutcome
import dev.servercontrolpanel.sdui.actionrunner.ActionRunner
import dev.servercontrolpanel.sdui.actionrunner.ScreenState
import dev.servercontrolpanel.sdui.confirm.ConfirmDestructiveComponent
import dev.servercontrolpanel.sdui.form.FormComponent

/**
 * The [ActionRunner] for the screen being rendered, or `null` (previews, screens
 * with no mutations). A composition local so [RenderComponent]'s signature stays stable.
 */
val LocalActionRunner = staticCompositionLocalOf<ActionRunner?> { null }

/**
 * The [ScreenState] for the screen being rendered. [FormComponent] and
 * [ActionComponent] use [ScreenState.confirmations] to find the
 * `confirm_destructive` declared for their action.
 */
val LocalScreenState = staticCompositionLocalOf<ScreenState?> { null }

/**
 * Called when the owner taps the [NeedsUpdateCard] shown for an unrecognized
 * critical component. Provided by the app shell; when `null` the card only
 * explains the gap.
 */
val LocalUpdateRequest = staticCompositionLocalOf<(() -> Unit)?> { null }

/**
 * What the renderer does with one [SduiComponent], decided purely from its
 * runtime type and its `critical` flag.
 */
enum class RenderPolicy {
    /** A recognized component type. */
    Render,

    /** A non-critical [SduiComponent.Unknown]: draws nothing, the rest of the screen renders. */
    Skip,

    /** A critical [SduiComponent.Unknown]: shows an update placeholder. */
    NeedsUpdate,
}

/**
 * Pure classification of [component] into a [RenderPolicy].
 *
 * No `else` branch on purpose: a new [SduiComponent] subclass must fail to
 * compile here rather than fall through silently.
 */
fun renderPolicyFor(component: SduiComponent): RenderPolicy = when (component) {
    is SduiComponent.Form -> RenderPolicy.Render
    is SduiComponent.Table -> RenderPolicy.Render
    is SduiComponent.ListComponent -> RenderPolicy.Render
    is SduiComponent.Detail -> RenderPolicy.Render
    is SduiComponent.Action -> RenderPolicy.Render
    is SduiComponent.Chart -> RenderPolicy.Render
    is SduiComponent.ConfirmDestructive -> RenderPolicy.Render
    is SduiComponent.Unknown -> if (component.critical) RenderPolicy.NeedsUpdate else RenderPolicy.Skip
}

/**
 * Dispatches [component] to its composable (exhaustive, no `else`).
 *
 * `ConfirmDestructive` renders nothing itself: it is a declaration indexed by
 * [ScreenState.confirmations], and the dialog is shown by the
 * [ActionComponent]/[FormComponent] dispatching that `action_id`.
 *
 * [onOutcome] bubbles mutation results up to the host, which decides whether the
 * screen must resync; the host then bumps [refreshKey] so a `Table` refetches its
 * rows (see [dev.servercontrolpanel.sdui.data.rememberComponentDataState]).
 */
@Composable
fun RenderComponent(
    component: SduiComponent,
    onOutcome: (ActionOutcome) -> Unit = {},
    refreshKey: Any? = null,
) {
    val actionRunner = LocalActionRunner.current
    val confirmations = LocalScreenState.current?.confirmations.orEmpty()
    when (component) {
        is SduiComponent.Form -> FormComponent(component, actionRunner, confirmations, onOutcome)
        is SduiComponent.Table -> dev.servercontrolpanel.sdui.table.TableComponent(component, actionRunner, confirmations, onOutcome, refreshKey)
        is SduiComponent.ListComponent -> dev.servercontrolpanel.sdui.list.ListComponent(component)
        is SduiComponent.Detail -> dev.servercontrolpanel.sdui.detail.DetailComponent(component)
        is SduiComponent.Action -> ActionComponent(component, actionRunner, confirmations, onOutcome)
        is SduiComponent.Chart -> dev.servercontrolpanel.sdui.chart.ChartComponent(component)
        is SduiComponent.ConfirmDestructive -> Unit
        is SduiComponent.Unknown ->
            if (component.critical) {
                val requestUpdate = LocalUpdateRequest.current
                NeedsUpdateCard(
                    unknownType = component.type,
                    onUpdateClick = requestUpdate ?: {},
                )
            }
        // Non-critical Unknown renders nothing; parseScreen already logged it once.
    }
}
