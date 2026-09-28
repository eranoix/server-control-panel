package dev.servercontrolpanel.feature.admin

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.core.sdui.SduiEnvelope
import dev.servercontrolpanel.data.sdui.SduiDataRepository
import dev.servercontrolpanel.data.sdui.SduiRepository
import dev.servercontrolpanel.data.sdui.SduiScreenPort
import dev.servercontrolpanel.data.sdui.SduiScreenResult
import dev.servercontrolpanel.sdui.actionrunner.ActionInvoker
import dev.servercontrolpanel.sdui.actionrunner.ActionOutcome
import dev.servercontrolpanel.sdui.actionrunner.ActionRunner
import dev.servercontrolpanel.sdui.actionrunner.ComponentDataFetcher
import dev.servercontrolpanel.sdui.actionrunner.ScreenRefetcher
import dev.servercontrolpanel.sdui.actionrunner.ScreenState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface AdminUiState {
    data object Loading : AdminUiState

    data class Unavailable(val message: String, val retry: () -> Unit) : AdminUiState

    data class Error(val message: String, val retry: () -> Unit) : AdminUiState

    data class Ready(
        val envelope: SduiEnvelope,
        val screenState: ScreenState,
        val actionRunner: ActionRunner,
        val refreshTick: Int = 0,
    ) : AdminUiState
}

class AdminViewModel(
    private val sectionId: String,
    private val screenPort: SduiScreenPort = SduiRepository(),
    private val componentDataFetcher: ComponentDataFetcher = ComponentDataFetcher(SduiDataRepository()::fetch),
) : ViewModel() {

    private val _uiState = MutableStateFlow<AdminUiState>(AdminUiState.Loading)
    val uiState: StateFlow<AdminUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun load() {
        _uiState.value = AdminUiState.Loading
        viewModelScope.launch {
            when (val result = screenPort.screen(sectionId)) {
                is SduiScreenResult.Success -> _uiState.value = buildReadyState(result.envelope)
                is SduiScreenResult.NotFound ->
                    _uiState.value = AdminUiState.Unavailable(
                        "This section is not available to your account. Pick another one in the selector above.",
                        ::load,
                    )
                is SduiScreenResult.Forbidden ->
                    _uiState.value = AdminUiState.Unavailable(
                        "You do not have permission to view this section. Pick another one in the selector above.",
                        ::load,
                    )
                is SduiScreenResult.Error ->
                    _uiState.value = AdminUiState.Error(result.reason, ::load)
            }
        }
    }

    private suspend fun buildReadyState(envelope: SduiEnvelope): AdminUiState.Ready {
        lateinit var screenState: ScreenState
        screenState = ScreenState(
            initialEnvelope = envelope,
            componentDataFetcher = componentDataFetcher,
            screenRefetcher = ScreenRefetcher {
                when (val refetched = screenPort.screen(sectionId)) {
                    is SduiScreenResult.Success -> refetched.envelope
                    else -> screenState.envelope
                }
            },
        )
        screenState.loadAll()
        val actionRunner = ActionRunner(
            invoker = ActionInvoker(screenPort::action),
            screenState = screenState,
        )
        return AdminUiState.Ready(envelope = screenState.envelope, screenState = screenState, actionRunner = actionRunner)
    }

    fun handleOutcome(outcome: ActionOutcome) {
        val current = _uiState.value as? AdminUiState.Ready ?: return
        when (outcome) {
            is ActionOutcome.Stale ->
                _uiState.value = current.copy(envelope = current.screenState.envelope, refreshTick = current.refreshTick + 1)
            is ActionOutcome.Invalidated, ActionOutcome.Patched ->
                _uiState.value = current.copy(refreshTick = current.refreshTick + 1)
            ActionOutcome.Gone, is ActionOutcome.ValidationFailed, is ActionOutcome.Failed ->
                Unit
        }
    }
}
