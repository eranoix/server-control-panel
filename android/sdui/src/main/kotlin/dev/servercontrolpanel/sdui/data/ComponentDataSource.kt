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

sealed interface ComponentDataState {
    data object Loading : ComponentDataState
    data class Error(val reason: String) : ComponentDataState
    data object Empty : ComponentDataState
    data class Data(val rows: List<JsonObject>) : ComponentDataState
}

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
