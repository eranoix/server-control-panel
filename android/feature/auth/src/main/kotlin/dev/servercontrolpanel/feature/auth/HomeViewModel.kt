package dev.servercontrolpanel.feature.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.dashboard.DashboardRepository
import dev.servercontrolpanel.data.dashboard.DashboardResult
import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.DashboardSource
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

sealed interface HomeUiState {
    data object Loading : HomeUiState

    data class Success(
        val snapshot: DashboardSnapshot,
        val refreshing: Boolean = false,
        val staleError: String? = null,
    ) : HomeUiState

    data class Error(val message: String) : HomeUiState
}

const val HOME_AUTO_REFRESH_MILLIS = 5_000L

class HomeViewModel(
    private val dashboard: DashboardSource = DashboardRepository(),
) : ViewModel() {

    private var writeWidgetSummary: ((dev.servercontrolpanel.data.dashboard.DashboardSnapshot) -> Unit)? = null

    fun publishSummaryWith(writer: (dev.servercontrolpanel.data.dashboard.DashboardSnapshot) -> Unit) {
        writeWidgetSummary = writer
    }

    private val _uiState = MutableStateFlow<HomeUiState>(HomeUiState.Loading)
    val uiState: StateFlow<HomeUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun load() {
        _uiState.value = HomeUiState.Loading
        fetch()
    }

    fun refresh() {
        _uiState.update { current ->
            if (current is HomeUiState.Success) current.copy(refreshing = true) else current
        }
        fetch()
    }

    fun autoRefresh() {
        if (_uiState.value !is HomeUiState.Success) return
        fetch()
    }

    private fun fetch() {
        viewModelScope.launch {
            when (val result = dashboard.load()) {
                is DashboardResult.Success -> {
                    _uiState.value = HomeUiState.Success(snapshot = result.snapshot)
                    writeWidgetSummary?.invoke(result.snapshot)
                }

                is DashboardResult.Error -> _uiState.update { current ->
                    if (current is HomeUiState.Success) {
                        current.copy(refreshing = false, staleError = result.reason)
                    } else {
                        HomeUiState.Error(result.reason)
                    }
                }
            }
        }
    }
}
