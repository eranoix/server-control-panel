package dev.servercontrolpanel.sdui.data

import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import dev.servercontrolpanel.core.sdui.SduiDataSource
import dev.servercontrolpanel.data.sdui.SduiDataRepository
import dev.servercontrolpanel.data.sdui.SduiDataResult
import dev.servercontrolpanel.sdui.registry.LocalScreenState
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject

/**
 * What a read component (table/list/detail/chart) has to show right now.
 * Every one of the four surfaces all four states explicitly — a
 * component rendering nothing while loading or on error would be visually
 * indistinguishable from a component the server omitted for RBAC reasons,
 * and only the server is allowed to make that decision.
 */
sealed interface ComponentDataState {
    data object Loading : ComponentDataState
    data class Error(val reason: String) : ComponentDataState
    data object Empty : ComponentDataState
    data class Data(val rows: List<JsonObject>) : ComponentDataState
}

/**
 * Maps a repository-layer [SduiDataResult] to the UI-facing [ComponentDataState].
 *
 * `internal/mobilebff/screens/scheduler_rows_test.go`'s `TestSchedulerRows_WireShape`
 * (plan 07-08) pins the one confirmed `rows_source` envelope so far:
 * `{"rows": [...]}}`, an object carrying the list under a named key — not a
 * bare JSON array. This function accepts that shape first. For any other
 * `rows_source`/`data_source`/`series_source` endpoint not yet backed by a
 * real handler, it falls back to the two shapes a future handler might still
 * return: a bare JSON array, or a single JSON object (wrapped as a
 * one-element list, the shape [dev.servercontrolpanel.sdui.detail.DetailComponent]
 * needs). Anything else is treated as a server-contract error rather than
 * guessed at.
 *
 * Pure function — this is what is unit-tested directly, with no Compose test
 * infrastructure needed.
 */
internal fun toComponentDataState(result: SduiDataResult): ComponentDataState = when (result) {
    is SduiDataResult.Error -> ComponentDataState.Error(result.reason)
    is SduiDataResult.Empty -> ComponentDataState.Empty
    is SduiDataResult.Success -> {
        val body = result.body
        val wrappedRows = (body as? JsonObject)?.get("rows") as? JsonArray
        when {
            wrappedRows != null && wrappedRows.isEmpty() -> ComponentDataState.Empty
            wrappedRows != null -> rowsOrError(wrappedRows)
            body is JsonArray && body.isEmpty() -> ComponentDataState.Empty
            body is JsonArray -> rowsOrError(body)
            body is JsonObject -> ComponentDataState.Data(listOf(body))
            else -> ComponentDataState.Error("Unsupported server response format.")
        }
    }
}

private fun rowsOrError(array: JsonArray): ComponentDataState {
    val rows = array.mapNotNull { it as? JsonObject }
    return if (rows.size == array.size) {
        ComponentDataState.Data(rows)
    } else {
        ComponentDataState.Error("The server response is not a list of objects.")
    }
}

/**
 * Fetches [dataSource] and exposes the result as [ComponentDataState].
 *
 * When the current screen provided a [dev.servercontrolpanel.sdui.registry.LocalScreenState]
 * (via [dev.servercontrolpanel.sdui.registry.LocalScreenState]), its
 * [dev.servercontrolpanel.sdui.actionrunner.ScreenState.componentDataFetcher] is used
 * instead of [repository] — the same seam [dev.servercontrolpanel.sdui.actionrunner.ScreenState]
 * uses for `loadAll`/`invalidate`. This is what keeps a read component honest
 * with an inert screen (`PayloadPreviewScreen`): half-inert would leave the
 * mutation path stubbed while reads still hit the real, authenticated
 * [SduiDataRepository]. A real screen that has not yet been wired with a
 * [dev.servercontrolpanel.sdui.registry.LocalScreenState] (none is, as of this plan)
 * falls back to [repository] unchanged, so this has no effect anywhere real
 * screens are rendered today. The server-supplied `endpoint`/`method` are
 * used verbatim either way — this function performs no client-side
 * formatting, filtering or aggregation of the rows it receives.
 *
 * [refreshKey] is an opt-in extra [produceState] key with no meaning of its
 * own to this function — it exists so a caller whose own state changed for a
 * reason this function cannot see (an action dispatched elsewhere on the
 * screen came back `Patched`/`Invalidated`) can force a fresh fetch by
 * passing a new value. Neither [dev.servercontrolpanel.sdui.actionrunner.ScreenState]
 * nor an [dev.servercontrolpanel.sdui.actionrunner.ActionOutcome] is observable by
 * Compose on its own (see `ScreenState`'s own doc comment), so this is the
 * seam that closes that gap without making `ScreenState`'s cache the
 * rendering source of truth.
 */
@Composable
fun rememberComponentDataState(
    dataSource: SduiDataSource,
    repository: SduiDataRepository = remember { SduiDataRepository() },
    refreshKey: Any? = null,
): State<ComponentDataState> {
    val overrideFetcher = LocalScreenState.current?.componentDataFetcher
    return produceState<ComponentDataState>(
        initialValue = ComponentDataState.Loading,
        dataSource,
        overrideFetcher,
        repository,
        refreshKey,
    ) {
        value = ComponentDataState.Loading
        val result = overrideFetcher?.fetch(dataSource) ?: repository.fetch(dataSource)
        value = toComponentDataState(result)
    }
}
