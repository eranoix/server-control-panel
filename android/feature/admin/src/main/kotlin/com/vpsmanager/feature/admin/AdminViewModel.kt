package com.vpsmanager.feature.admin

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.core.sdui.SduiEnvelope
import com.vpsmanager.data.sdui.SduiDataRepository
import com.vpsmanager.data.sdui.SduiRepository
import com.vpsmanager.data.sdui.SduiScreenPort
import com.vpsmanager.data.sdui.SduiScreenResult
import com.vpsmanager.sdui.actionrunner.ActionInvoker
import com.vpsmanager.sdui.actionrunner.ActionOutcome
import com.vpsmanager.sdui.actionrunner.ActionRunner
import com.vpsmanager.sdui.actionrunner.ComponentDataFetcher
import com.vpsmanager.sdui.actionrunner.ScreenRefetcher
import com.vpsmanager.sdui.actionrunner.ScreenState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * What [AdminScreen] renders for one section id. Never named after any
 * section — a new section produces the exact same three states, only the
 * envelope's own content differs.
 */
sealed interface AdminUiState {
    data object Loading : AdminUiState

    /**
     * The section is not available to this account — the 404 (or 403) from
     * `/screens/{id}`.
     *
     * SEPARATE from [Error] on purpose. The server answers 404 rather than 403
     * so as not to confirm the existence of a screen to someone who may not
     * see it (see `internal/mobilebff/handlers_screens.go`), and the picker's
     * catalogue is only a HINT: what authorises is the fetch. So this case is
     * the normal outcome of "the permission changed between the listing and
     * the tap", not a failure. Painting it as an error would train the user to
     * ignore real errors; the screen shows a discreet notice and keeps the
     * picker within reach.
     */
    data class Unavailable(val message: String, val retry: () -> Unit) : AdminUiState

    data class Error(val message: String, val retry: () -> Unit) : AdminUiState

    /**
     * [refreshTick] changes every time an [ActionOutcome] this ViewModel
     * observes should make a rendered component re-pull its own data (a row
     * action landed) — `:sdui`'s per-component data cache (see
     * `rememberComponentDataState`) is keyed on it, since neither a
     * [ScreenState] mutation nor a `Patched`/`Invalidated` outcome carries
     * enough on its own to invalidate a running `produceState` block.
     */
    data class Ready(
        val envelope: SduiEnvelope,
        val screenState: ScreenState,
        val actionRunner: ActionRunner,
        val refreshTick: Int = 0,
    ) : AdminUiState
}

/**
 * Owns the section-agnostic plumbing every SDUI-described screen needs:
 * fetch the descriptor, hold the [ScreenState]/[ActionRunner] pair for its
 * lifetime, and refetch when an action reports the view has drifted. Holds
 * no section-specific behaviour of any kind — [sectionId] is opaque data,
 * never branched on.
 *
 * The two dependencies come in as SEAMS, not as concrete classes: in
 * production the defaults below assemble the real [SduiRepository]/
 * [SduiDataRepository] pair, and a test in this module fakes at the data
 * boundary — with no need to stand up an HTTP server inside `feature/`, which
 * the gate `scripts/check-mobile-bff-only.sh` forbids outside `:data`.
 *
 * The two seams have different shapes on purpose: [SduiScreenPort] is a
 * two-member interface because this ViewModel needs both of them (read the
 * descriptor and dispatch an action), whereas the component data boundary
 * already had its seam ready in [ComponentDataFetcher] — reusing it avoids
 * two names for the same boundary, and it is exactly the type [ScreenState]
 * consumes, so no conversion is left in between.
 */
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

    /** Re-runs the initial fetch from scratch — the [AdminUiState.Error] retry action. */
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
                // Best-effort resync: a failed refetch keeps the screen on
                // its last known-good envelope rather than crashing the
                // action dispatch that triggered it. The next explicit
                // "tentar novamente" (or a later successful action) surfaces
                // the failure for real.
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

    /**
     * Bubbled up from every action dispatched anywhere on the current
     * screen (row actions, form submits, standalone buttons alike — this
     * has no idea which). [ActionOutcome.Stale] has already been resynced
     * onto [ScreenState] by [ActionRunner] itself; this only needs to make
     * that mutation visible by re-emitting a fresh [AdminUiState.Ready] and
     * bumping [AdminUiState.Ready.refreshTick] so components reload.
     */
    fun handleOutcome(outcome: ActionOutcome) {
        val current = _uiState.value as? AdminUiState.Ready ?: return
        when (outcome) {
            is ActionOutcome.Stale ->
                _uiState.value = current.copy(envelope = current.screenState.envelope, refreshTick = current.refreshTick + 1)
            is ActionOutcome.Invalidated, ActionOutcome.Patched ->
                _uiState.value = current.copy(refreshTick = current.refreshTick + 1)
            ActionOutcome.Gone, is ActionOutcome.ValidationFailed, is ActionOutcome.Failed ->
                Unit // surfaced inline by the component that dispatched the action; nothing screen-wide to do here.
        }
    }
}
