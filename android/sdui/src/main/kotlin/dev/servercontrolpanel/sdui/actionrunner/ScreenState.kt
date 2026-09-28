package dev.servercontrolpanel.sdui.actionrunner

import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.core.sdui.SduiDataSource
import dev.servercontrolpanel.core.sdui.SduiEnvelope
import dev.servercontrolpanel.core.sdui.SduiScreen
import dev.servercontrolpanel.data.sdui.SduiDataResult
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

fun interface ComponentDataFetcher {
    suspend fun fetch(dataSource: SduiDataSource): SduiDataResult
}

fun interface ScreenRefetcher {
    suspend fun refetch(): SduiEnvelope
}

private fun SduiComponent.dataSourceOrNull(): SduiDataSource? = when (this) {
    is SduiComponent.Table -> rowsSource
    is SduiComponent.ListComponent -> rowsSource
    is SduiComponent.Detail -> dataSource
    is SduiComponent.Chart -> seriesSource
    is SduiComponent.Form -> null
    is SduiComponent.Action -> null
    is SduiComponent.ConfirmDestructive -> null
    is SduiComponent.Unknown -> null
}

private fun JsonObject.idOrNull(): String? = (this["id"] as? JsonPrimitive)?.content

private fun rowsFromResult(result: SduiDataResult): List<JsonObject> = when (result) {
    is SduiDataResult.Success -> when (val body = result.body) {
        is JsonArray -> body.mapNotNull { it as? JsonObject }
        is JsonObject -> listOf(body)
        else -> emptyList()
    }
    is SduiDataResult.Empty -> emptyList()
    is SduiDataResult.Error -> emptyList()
}

class ScreenState(
    initialEnvelope: SduiEnvelope,
    val componentDataFetcher: ComponentDataFetcher,
    private val screenRefetcher: ScreenRefetcher,
) {
    var envelope: SduiEnvelope = initialEnvelope
        private set

    private val componentData = mutableMapOf<String, List<JsonObject>>()

    val confirmations: Map<String, SduiComponent.ConfirmDestructive>
        get() = confirmationsFor(envelope.screen)

    fun rowsFor(componentId: String): List<JsonObject> = componentData[componentId].orEmpty()

    suspend fun loadAll() {
        envelope.screen.components.forEach { component ->
            component.dataSourceOrNull()?.let { dataSource ->
                componentData[component.id] = rowsFromResult(componentDataFetcher.fetch(dataSource))
            }
        }
    }

    fun applyPatch(patch: JsonObject): Boolean {
        val patchId = patch.idOrNull() ?: return false
        var replacedAny = false
        componentData.keys.toList().forEach { componentId ->
            val rows = componentData.getValue(componentId)
            val index = rows.indexOfFirst { it.idOrNull() == patchId }
            if (index >= 0) {
                componentData[componentId] = rows.toMutableList().apply { set(index, patch) }
                replacedAny = true
            }
        }
        return replacedAny
    }

    suspend fun invalidate(ids: List<String>) {
        envelope.screen.components
            .filter { it.id in ids }
            .forEach { component ->
                component.dataSourceOrNull()?.let { dataSource ->
                    componentData[component.id] = rowsFromResult(componentDataFetcher.fetch(dataSource))
                }
            }
    }

    suspend fun refetchScreen() {
        envelope = screenRefetcher.refetch()
        componentData.clear()
        loadAll()
    }
}

fun confirmationsFor(screen: SduiScreen): Map<String, SduiComponent.ConfirmDestructive> =
    screen.components.filterIsInstance<SduiComponent.ConfirmDestructive>().associateBy { it.actionId }

fun confirmationFor(screen: SduiScreen, actionId: String): SduiComponent.ConfirmDestructive? =
    confirmationsFor(screen)[actionId]
