package com.vpsmanager.sdui.actionrunner

import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.core.sdui.SduiDataSource
import com.vpsmanager.core.sdui.SduiEnvelope
import com.vpsmanager.core.sdui.SduiScreen
import com.vpsmanager.data.sdui.SduiDataResult
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Fetches one component's `rows_source`/`data_source`/`series_source`. A
 * `fun interface` seam so tests can supply a counting fake without a mocking
 * library (none is present in this codebase) — production passes
 * `SduiDataRepository::fetch` directly, since a method reference already
 * satisfies a SAM interface.
 */
fun interface ComponentDataFetcher {
    suspend fun fetch(dataSource: SduiDataSource): SduiDataResult
}

/**
 * Refetches the whole screen envelope — the resync path used when the
 * server tells the client its view has gone stale (a 403 on an action, see
 * [ActionRunner]).
 */
fun interface ScreenRefetcher {
    suspend fun refetch(): SduiEnvelope
}

/** The data-bearing endpoint of [component], or null for the three
 *  mutation-only component kinds that never cache rows of their own. No
 *  `else` branch: a 9th [SduiComponent] subclass must be taught what its
 *  data source is (or that it has none) before this compiles. */
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

/**
 * Holds the current [envelope] plus per-component cached rows, and is the
 * single place [ActionRunner] applies a `patch`/`invalidate` server
 * response (the `<interfaces>` contract in plan 07-06's PLAN.md). A plain
 * class with a coroutine-friendly API — unit-testable without any Compose
 * test infrastructure.
 */
class ScreenState(
    initialEnvelope: SduiEnvelope,
    /** Public so [com.vpsmanager.sdui.data.rememberComponentDataState] can
     *  route a read component's fetch through the exact same source this
     *  screen uses for `loadAll`/`invalidate` — a screen built with an inert
     *  fetcher (see `PayloadPreviewScreen`) stays inert for reads too, not
     *  only for the action-dispatch path. */
    val componentDataFetcher: ComponentDataFetcher,
    private val screenRefetcher: ScreenRefetcher,
) {
    var envelope: SduiEnvelope = initialEnvelope
        private set

    private val componentData = mutableMapOf<String, List<JsonObject>>()

    /** Every `confirm_destructive` on the current screen, indexed by the
     *  `action_id` it declares — built fresh from [envelope] on every read,
     *  so it is always in sync after a [refetchScreen]. */
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

    /**
     * Replaces, wherever it is cached, the row/resource whose own `id`
     * field matches [patch]'s `id`. Returns whether any cached component
     * actually held that id — a patch matching nothing cached is not an
     * error (the resource may simply not be shown on this screen), but is
     * worth the caller being able to tell.
     */
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

    /** Refetches exactly the components named in [ids] — never the whole
     *  screen — per the server's `invalidate` instruction. */
    suspend fun invalidate(ids: List<String>) {
        envelope.screen.components
            .filter { it.id in ids }
            .forEach { component ->
                component.dataSourceOrNull()?.let { dataSource ->
                    componentData[component.id] = rowsFromResult(componentDataFetcher.fetch(dataSource))
                }
            }
    }

    /** Refetches the whole envelope — the resync path for a 403
     *  [ActionOutcome.Stale]: the server's descriptor and this viewer's
     *  permissions have diverged, and the client's job is to resync rather
     *  than to argue. */
    suspend fun refetchScreen() {
        envelope = screenRefetcher.refetch()
        componentData.clear()
        loadAll()
    }
}

/**
 * Pure lookup: every `confirm_destructive` component in [screen], indexed
 * by the `action_id` it declares. A row action and a standalone action
 * resolve identically through this map — confirmation is a property of the
 * action id, not of where the button lives.
 */
fun confirmationsFor(screen: SduiScreen): Map<String, SduiComponent.ConfirmDestructive> =
    screen.components.filterIsInstance<SduiComponent.ConfirmDestructive>().associateBy { it.actionId }

/** [confirmationsFor] narrowed to one `actionId` — null means the server
 *  declared no confirmation for it, in which case the client dispatches
 *  directly and the server's own unconfirmed-destructive check (plan 07-04)
 *  is the only thing standing between that dispatch and a 422. */
fun confirmationFor(screen: SduiScreen, actionId: String): SduiComponent.ConfirmDestructive? =
    confirmationsFor(screen)[actionId]
