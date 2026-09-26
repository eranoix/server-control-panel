package com.vpsmanager.sdui.registry

import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.sdui.action.ActionComponent
import com.vpsmanager.sdui.actionrunner.ActionOutcome
import com.vpsmanager.sdui.actionrunner.ActionRunner
import com.vpsmanager.sdui.actionrunner.ScreenState
import com.vpsmanager.sdui.confirm.ConfirmDestructiveComponent
import com.vpsmanager.sdui.form.FormComponent

/**
 * The live [ActionRunner] for the screen currently being rendered, or `null`
 * before one has been provided (a preview, or a screen with no mutation
 * components). Threaded through composition rather than as a [RenderComponent]
 * parameter so this file's dispatch signature — and `SduiScreenRenderer.kt`,
 * which calls it — never has to change shape as mutation support was added.
 */
val LocalActionRunner = staticCompositionLocalOf<ActionRunner?> { null }

/**
 * The live [ScreenState] for the screen currently being rendered — its
 * [ScreenState.confirmations] index is how [FormComponent]/[ActionComponent]
 * find the `confirm_destructive` declared for the action they are about to
 * dispatch, without either composable knowing about the rest of the screen.
 */
val LocalScreenState = staticCompositionLocalOf<ScreenState?> { null }

/**
 * What to do when the owner taps the [NeedsUpdateCard] — the card that appears
 * in place of a CRITICAL component this build does not recognize.
 *
 * Threaded through a composition local for the same reason as
 * [LocalActionRunner]: the card is born arbitrarily deep inside a payload
 * described by the server, and carrying a lambda down to it by parameter would
 * change four public signatures (`SduiScreen`, `RenderComponent`, and the two
 * screens that call them) just to carry a function.
 *
 * `null` (the default) keeps the previous behaviour: the card stays honest
 * about the gap, but does not pretend to resolve it. What installs the real
 * value is the app shell, which is the one that has the update coordinator.
 */
val LocalUpdateRequest = staticCompositionLocalOf<(() -> Unit)?> { null }

/**
 * What the renderer does with one [SduiComponent], decided purely from its
 * runtime type and its `critical` flag.
 */
enum class RenderPolicy {
    /** A recognized type (or a non-critical [SduiComponent.Unknown]'s
     *  sibling case is [Skip] instead) that has a real or TODO composable. */
    Render,

    /** A non-critical [SduiComponent.Unknown] — the rest of the screen still
     *  renders; this component draws nothing. */
    Skip,

    /** A critical [SduiComponent.Unknown] — the server considered this
     *  essential, so a placeholder is shown instead of nothing. */
    NeedsUpdate,
}

/**
 * Pure classification of [component] into a [RenderPolicy]. No composition,
 * no side effects — this is what `RegistryDispatchTest` exercises directly,
 * without a Compose test rule.
 *
 * The `when` below has **no `else` branch on purpose**. [SduiComponent] is a
 * sealed interface with exactly 8 direct subclasses (7 known + [Unknown]);
 * omitting `else` means the Kotlin compiler enforces that every subclass is
 * handled here. If a 9th subclass is ever added to the sealed hierarchy, this
 * function fails to compile until it is taught what to do with it — a 9th
 * type can never silently fall through to a default at runtime.
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
 * Dispatches [component] to its composable by the same exhaustive `when` as
 * [renderPolicyFor] (again: **no `else`**, same compile-time guarantee).
 *
 * `Form` and `Action` read [LocalActionRunner]/[LocalScreenState] from
 * composition to dispatch and to look up whether the action they are about
 * to run requires confirmation. `ConfirmDestructive` renders nothing of its
 * own here — as plan 07-06 states, it is a declaration indexed by
 * [ScreenState.confirmations], not a standalone piece of UI; the confirmation
 * dialog it describes is shown by whichever [ActionComponent]/[FormComponent]
 * is about to dispatch the `action_id` it declares.
 *
 * [onOutcome] is bubbled from every mutation-capable branch (`Form`,
 * `Action`, `Table`'s row actions) up to whatever hosts this screen — a host
 * ViewModel is the one thing that can decide a `Stale`/`Invalidated` outcome
 * should make the rest of the screen visibly resync. [refreshKey] flows the
 * other way: a host bumps it once it has reacted to such an outcome, so a
 * `Table`'s own row fetch (independent of [ScreenState]'s cache — see
 * [com.vpsmanager.sdui.data.rememberComponentDataState]) re-runs.
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
        is SduiComponent.Table -> com.vpsmanager.sdui.table.TableComponent(component, actionRunner, confirmations, onOutcome, refreshKey)
        is SduiComponent.ListComponent -> com.vpsmanager.sdui.list.ListComponent(component)
        is SduiComponent.Detail -> com.vpsmanager.sdui.detail.DetailComponent(component)
        is SduiComponent.Action -> ActionComponent(component, actionRunner, confirmations, onOutcome)
        is SduiComponent.Chart -> com.vpsmanager.sdui.chart.ChartComponent(component)
        is SduiComponent.ConfirmDestructive -> Unit
        is SduiComponent.Unknown ->
            if (component.critical) {
                val requestUpdate = LocalUpdateRequest.current
                NeedsUpdateCard(
                    unknownType = component.type,
                    onUpdateClick = requestUpdate ?: {},
                )
            }
        // else: non-critical Unknown renders nothing. It was already logged
        // once, at parse time, by core.sdui.parseScreen — logging it again
        // here would double-count the same skip.
    }
}
