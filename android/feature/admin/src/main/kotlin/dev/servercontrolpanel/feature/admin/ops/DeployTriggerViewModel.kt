package dev.servercontrolpanel.feature.admin.ops

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.events.EventsSubscriber
import dev.servercontrolpanel.data.events.MobileEvent
import dev.servercontrolpanel.data.ops.DeployStatusResult
import dev.servercontrolpanel.data.ops.OpsRepository
import dev.servercontrolpanel.data.ops.OpsSource
import dev.servercontrolpanel.data.ops.TriggerDeployResult
import dev.servercontrolpanel.data.ops.decodeDeployEvent
import dev.servercontrolpanel.data.session.SessionRepository
import dev.servercontrolpanel.data.session.SessionResult
import dev.servercontrolpanel.data.session.SessionSource
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

private val TERMINAL_STATUSES = setOf("done", "failed", "cancelled", "interrupted")

sealed interface DeployTriggerUiState {
    data object Idle : DeployTriggerUiState

    data object AwaitingConfirmation : DeployTriggerUiState

    data class TriggerFailed(val message: String) : DeployTriggerUiState

    data class InProgress(
        val jobId: String,
        val phase: String,
        val logLines: List<String>,
        val progress: Int,
        val step: String?,
    ) : DeployTriggerUiState

    data class Outcome(val status: String, val error: String?) : DeployTriggerUiState
}

class DeployTriggerViewModel(
    private val eventsClient: EventsSubscriber,
    private val repository: OpsSource = OpsRepository(),
    private val sessionRepository: SessionSource = SessionRepository(),
    private val trackOffScreen: ((jobId: String) -> Unit)? = null,
) : ViewModel() {

    private val _uiState = MutableStateFlow<DeployTriggerUiState>(DeployTriggerUiState.Idle)
    val uiState: StateFlow<DeployTriggerUiState> = _uiState.asStateFlow()

    private val _isAdmin = MutableStateFlow<Boolean?>(null)
    val isAdmin: StateFlow<Boolean?> = _isAdmin.asStateFlow()

    private var streamJob: Job? = null

    init {
        viewModelScope.launch {
            _isAdmin.value = when (val result = sessionRepository.getMe()) {
                is SessionResult.Success -> result.isAdmin
                is SessionResult.Empty, is SessionResult.Error -> false
            }
        }
    }

    fun requestConfirmation() {
        when (_uiState.value) {
            is DeployTriggerUiState.InProgress -> Unit
            else -> _uiState.value = DeployTriggerUiState.AwaitingConfirmation
        }
    }

    fun dismissConfirmation() {
        if (_uiState.value is DeployTriggerUiState.AwaitingConfirmation) {
            _uiState.value = DeployTriggerUiState.Idle
        }
    }

    fun confirmDeploy() {
        if (_uiState.value !is DeployTriggerUiState.AwaitingConfirmation) return
        viewModelScope.launch {
            when (val result = repository.triggerDeploy()) {
                is TriggerDeployResult.Success -> {
                    trackOffScreen?.invoke(result.jobId)
                    startStreaming(result.jobId)
                }
                is TriggerDeployResult.Error -> _uiState.value = DeployTriggerUiState.TriggerFailed(result.reason)
            }
        }
    }

    private fun startStreaming(jobId: String) {
        _uiState.value = DeployTriggerUiState.InProgress(jobId = jobId, phase = "queued", logLines = emptyList(), progress = 0, step = null)
        streamJob = eventsClient.subscribe("deploy.$jobId")
            .onEach { event -> applyDeployEvent(jobId, event) }
            .launchIn(viewModelScope)
    }

    private fun applyDeployEvent(jobId: String, event: MobileEvent) {
        val current = _uiState.value as? DeployTriggerUiState.InProgress ?: return
        val decoded = decodeDeployEvent(event.data) ?: return

        val newLine = decoded.logLine
        val nextLines = if (decoded.type == "log" && newLine != null) {
            current.logLines + newLine
        } else {
            current.logLines
        }
        _uiState.value = current.copy(
            logLines = nextLines,
            phase = decoded.status ?: current.phase,
            progress = if (decoded.type == "progress") decoded.progress else current.progress,
            step = decoded.step ?: current.step,
        )

        if (decoded.type == "status" && decoded.status in TERMINAL_STATUSES) {
            streamJob?.cancel()
            finalize(jobId)
        }
    }

    private fun finalize(jobId: String) {
        viewModelScope.launch {
            when (val result = repository.fetchDeployStatus(jobId)) {
                is DeployStatusResult.Success -> {
                    _uiState.value = DeployTriggerUiState.Outcome(result.status.status, result.status.error)
                }
                is DeployStatusResult.Error -> Unit
            }
        }
    }
}
