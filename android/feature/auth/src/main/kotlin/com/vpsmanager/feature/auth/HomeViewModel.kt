package com.vpsmanager.feature.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.dashboard.DashboardRepository
import com.vpsmanager.data.dashboard.DashboardResult
import com.vpsmanager.data.dashboard.DashboardSnapshot
import com.vpsmanager.data.dashboard.DashboardSource
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * The Home screen's state.
 *
 * ## Why there is no "empty" state
 * The old screen had an `Empty` for "the server returned no e-mail address". An
 * operations dashboard is never empty: no alert is the good news, and the rest
 * of the cards still have content. A missing identity became a detail in the
 * footer, not a state of the whole screen.
 *
 * ## Why [Success] carries `refreshing` and `staleError`
 * Reloading must not flash the screen back to the skeleton: the operator would
 * be reading a number and it would vanish. And a reload that fails must not
 * erase the previous picture — but it must ALSO not keep quiet, or the old
 * number passes for the current one. So the picture stays, stamped with the
 * time it was fetched and a warning that the last attempt failed.
 */
sealed interface HomeUiState {
    data object Loading : HomeUiState

    data class Success(
        val snapshot: DashboardSnapshot,
        val refreshing: Boolean = false,
        val staleError: String? = null,
    ) : HomeUiState

    data class Error(val message: String) : HomeUiState
}

/**
 * Interval of the automatic refresh while the screen is VISIBLE.
 *
 * `/ops/status` is cached for 3 s on the server (`internal/mobilebff/ops_health.go`),
 * so asking every 5 s costs, at worst, a couple of reads of `/proc` — and
 * guarantees no number on screen is more than 5 s old. Outside the RESUMED
 * state the loop does not run: a backgrounded dashboard does not spend radio.
 */
const val HOME_AUTO_REFRESH_MILLIS = 5_000L

/**
 * Drives the Home screen. The only source is [DashboardSource], which brings
 * `/ops/status`, `/me`, `/deploy/apps` and `/scheduler/jobs` together in one
 * parallel fetch.
 */
class HomeViewModel(
    private val dashboard: DashboardSource = DashboardRepository(),
) : ViewModel() {

    /**
     * How to publish the summary for the home-screen widget.
     *
     * Injected and nullable because this ViewModel is tested on the plain JVM
     * and the summary's destination is `SharedPreferences` — forcing every
     * dashboard test to stand up an Android context just to exercise a
     * threshold decision would be paying dearly for nothing.
     */
    private var gravarResumoDoWidget: ((com.vpsmanager.data.dashboard.DashboardSnapshot) -> Unit)? = null

    /** Switches summary publishing on. Called by the screen, which has a context. */
    fun publicarResumoCom(escritor: (com.vpsmanager.data.dashboard.DashboardSnapshot) -> Unit) {
        gravarResumoDoWidget = escritor
    }

    private val _uiState = MutableStateFlow<HomeUiState>(HomeUiState.Loading)
    val uiState: StateFlow<HomeUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    /** First load and "try again": may show the skeleton. */
    fun load() {
        _uiState.value = HomeUiState.Loading
        fetch()
    }

    /**
     * Pull-to-refresh: marks the reload visibly (the gesture's indicator) but
     * NEVER discards the picture already on screen.
     */
    fun refresh() {
        _uiState.update { current ->
            if (current is HomeUiState.Success) current.copy(refreshing = true) else current
        }
        fetch()
    }

    /**
     * The screen loop's automatic refresh: silent by definition — no indicator,
     * no flashing. If the app is still loading or in a hard error, it does
     * nothing: it is the operator who decides to leave those states, and a loop
     * retrying every 5 s on top of an error message would only make the message
     * shudder.
     */
    fun autoRefresh() {
        if (_uiState.value !is HomeUiState.Success) return
        fetch()
    }

    private fun fetch() {
        viewModelScope.launch {
            when (val result = dashboard.load()) {
                is DashboardResult.Success -> {
                    _uiState.value = HomeUiState.Success(snapshot = result.snapshot)
                    // THE HOME-SCREEN WIDGET READS FROM HERE.
                    //
                    // Write on the good read, and only on that one: the widget
                    // is drawn by another process the system wakes without
                    // warning, and making it fetch data would require the
                    // network plus a session that may have expired and nobody
                    // there to see the error — a recipe for a widget stuck on
                    // "loading" forever. The honest consequence is that someone
                    // who has not opened the app for a day reads "more than a
                    // day ago" on the widget, rather than an invented number.
                    gravarResumoDoWidget?.invoke(result.snapshot)
                }

                is DashboardResult.Error -> _uiState.update { current ->
                    // There was already a dashboard on screen: keep it and say
                    // the last attempt failed. Only when there is nothing at all
                    // does the whole screen become an error message.
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
