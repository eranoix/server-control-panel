package com.vpsmanager.feature.admin.ops

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.events.EventsSubscriber
import com.vpsmanager.data.events.MobileEvent
import com.vpsmanager.data.ops.OpsRepository
import com.vpsmanager.data.ops.OpsSnapshot
import com.vpsmanager.data.ops.OpsSource
import com.vpsmanager.data.ops.OpsStatusResult
import com.vpsmanager.data.ops.decodeOpsSnapshot
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.launch

sealed interface OpsDashboardUiState {
    data object Loading : OpsDashboardUiState
    data class LoadError(val message: String) : OpsDashboardUiState
    data class Success(val snapshot: OpsSnapshot) : OpsDashboardUiState
}

/**
 * Fetches `GET /api/mobile/v1/ops/status` once for initial state, then subscribes to the
 * `"ops.health"` live channel for every update thereafter — the dashboard never polls.
 * Collection of [EventsSubscriber.subscribe]'s [kotlinx.coroutines.flow.Flow] is tied to
 * [viewModelScope], so it is cancelled — which is this API's unsubscribe signal — the moment
 * this ViewModel is cleared (screen leaves composition).
 */
class OpsDashboardViewModel(
    private val eventsClient: EventsSubscriber,
    private val repository: OpsSource = OpsRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<OpsDashboardUiState>(OpsDashboardUiState.Loading)
    val uiState: StateFlow<OpsDashboardUiState> = _uiState.asStateFlow()

    init {
        refresh()
        subscribeLive()
    }

    fun refresh() {
        _uiState.value = OpsDashboardUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = repository.fetchStatus()) {
                is OpsStatusResult.Success -> OpsDashboardUiState.Success(result.snapshot)
                is OpsStatusResult.Error -> OpsDashboardUiState.LoadError(result.reason)
            }
        }
    }

    private fun subscribeLive() {
        eventsClient.subscribe("ops.health")
            .onEach(::applyLiveEvent)
            .launchIn(viewModelScope)
    }

    private fun applyLiveEvent(event: MobileEvent) {
        val snapshot = decodeOpsSnapshot(event.data) ?: return
        _uiState.value = OpsDashboardUiState.Success(snapshot)
    }
}
