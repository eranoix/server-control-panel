package dev.servercontrolpanel.feature.admin.ops

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.events.EventsSubscriber
import dev.servercontrolpanel.data.events.MobileEvent
import dev.servercontrolpanel.data.ops.OpsRepository
import dev.servercontrolpanel.data.ops.OpsSnapshot
import dev.servercontrolpanel.data.ops.OpsSource
import dev.servercontrolpanel.data.ops.OpsStatusResult
import dev.servercontrolpanel.data.ops.decodeOpsSnapshot
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
