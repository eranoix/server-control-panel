package com.vpsmanager.feature.admin.ops

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.events.EventsSubscriber
import com.vpsmanager.data.events.MobileEvent
import com.vpsmanager.data.ops.DeployStatusResult
import com.vpsmanager.data.ops.OpsRepository
import com.vpsmanager.data.ops.OpsSource
import com.vpsmanager.data.ops.TriggerDeployResult
import com.vpsmanager.data.ops.decodeDeployEvent
import com.vpsmanager.data.session.SessionRepository
import com.vpsmanager.data.session.SessionResult
import com.vpsmanager.data.session.SessionSource
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** `internal/queue.Status` values that mean the job will never emit another event. */
private val TERMINAL_STATUSES = setOf("done", "failed", "cancelled", "interrupted")

sealed interface DeployTriggerUiState {
    /** Nothing in flight; the confirmation dialog has not been opened yet (or a prior run finished). */
    data object Idle : DeployTriggerUiState

    /** The confirmation dialog is open, awaiting the user's decision. */
    data object AwaitingConfirmation : DeployTriggerUiState

    /** `POST /ops/deploy` itself failed (never a rejected/cancelled deploy — see [Outcome] for that). */
    data class TriggerFailed(val message: String) : DeployTriggerUiState

    /**
     * A job id exists and this screen is subscribed to `deploy.<jobId>`. [phase] starts at
     * `"queued"` the instant the trigger call returns — `agentctl deploy`'s `flock -w 600` gives
     * no intermediate signal while a prior deploy holds the lock, so a job can sit queued with
     * zero events for up to 10 minutes. Rendering `"queued"` explicitly (rather than a bare
     * spinner) is what keeps that wait from reading as a frozen screen; `phase` only moves to
     * `"running"` once the first `status` event actually arrives.
     */
    data class InProgress(
        val jobId: String,
        val phase: String,
        val logLines: List<String>,
        val progress: Int,
        val step: String?,
    ) : DeployTriggerUiState

    /** The job reached a terminal status. `status`/`error` are rendered verbatim — never inferred. */
    data class Outcome(val status: String, val error: String?) : DeployTriggerUiState
}

/**
 * The only call site for [OpsSource.triggerDeploy] is [confirmDeploy], itself only reachable
 * from [AwaitingConfirmation] — there is no path in this ViewModel that can fire a deploy
 * without the confirmation dialog first opening.
 */
class DeployTriggerViewModel(
    private val eventsClient: EventsSubscriber,
    private val repository: OpsSource = OpsRepository(),
    private val sessionRepository: SessionSource = SessionRepository(),
    /**
     * How to follow the deploy once this screen is gone.
     *
     * Injected and nullable on purpose: this ViewModel is tested on the plain
     * JVM, and the follow-along is a `WorkManager` — constructing one here
     * would force every test to stand up Android infrastructure just to
     * exercise a decision that has nothing to do with it.
     */
    private val acompanharForaDaTela: ((jobId: String) -> Unit)? = null,
) : ViewModel() {

    private val _uiState = MutableStateFlow<DeployTriggerUiState>(DeployTriggerUiState.Idle)
    val uiState: StateFlow<DeployTriggerUiState> = _uiState.asStateFlow()

    /**
     * `null` while the check is in flight, then the real answer. The server is the actual
     * enforcement point (the `httpx.IsAdmin` gate on `POST /ops/deploy`) — this is only the
     * UX-level "don't even show the button" guard, re-checked on every load rather than cached
     * from a stale session.
     */
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
                    // FOLLOW IT OFF-SCREEN. `startStreaming` shows progress
                    // HERE, and only for as long as this screen lives — but
                    // whoever fires a deploy from a phone almost always puts
                    // the phone away straight afterwards, and Android 15 cuts
                    // the backgrounded process's network in about 5.7 s.
                    // Without this, the deploy went up to ten minutes with no
                    // signal at all.
                    acompanharForaDaTela?.invoke(result.jobId)
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

    /**
     * `queue.Event` (the live frame) carries `status` but never the failure tail-buffer text —
     * only `GET /ops/deploy/{jobId}` (`DeployStatusOutputBody.error`) does. This fallback also
     * covers the socket-disconnected-mid-deploy case: reconnecting resubscribes to the same
     * channel automatically, but if the terminal event was missed outright, this poll is what
     * still produces a real outcome instead of a permanently stuck screen.
     */
    private fun finalize(jobId: String) {
        viewModelScope.launch {
            when (val result = repository.fetchDeployStatus(jobId)) {
                is DeployStatusResult.Success -> {
                    _uiState.value = DeployTriggerUiState.Outcome(result.status.status, result.status.error)
                }
                is DeployStatusResult.Error -> Unit // last InProgress state stands; the screen offers a retry fetch
            }
        }
    }
}
