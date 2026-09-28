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

val LocalActionRunner = staticCompositionLocalOf<ActionRunner?> { null }

val LocalScreenState = staticCompositionLocalOf<ScreenState?> { null }

val LocalUpdateRequest = staticCompositionLocalOf<(() -> Unit)?> { null }

enum class RenderPolicy {
    Render,

    Skip,

    NeedsUpdate,
}

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
    }
}
