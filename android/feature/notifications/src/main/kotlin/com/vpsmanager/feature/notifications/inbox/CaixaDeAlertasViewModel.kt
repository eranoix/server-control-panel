package com.vpsmanager.feature.notifications.inbox

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.ops.OpsAlert
import com.vpsmanager.data.ops.OpsRepository
import com.vpsmanager.data.ops.OpsStatusResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * The alerts that are firing, plus this device's "seen" marks.
 *
 * ## Why it reuses `/ops/status` instead of a new route
 *
 * `/ops/status` already returns the firing alerts, already has a 3 s cache on
 * the server and is already called by the Home screen. Asking for a new route
 * for the same data would create two sources that diverge on the day only one
 * of them gets fixed — which is exactly the structural defect that killed the
 * previous attempt at an app.
 *
 * ## A failure does not wipe the list
 *
 * A reload that fails PRESERVES the previous alerts and says that it failed.
 * The opposite — clearing the screen because the network blinked — would make
 * the inbox look empty, and "no alerts" is the one sentence this component can
 * never say by mistake.
 */
internal class CaixaDeAlertasViewModel(
    application: Application,
    repositorioParaTeste: OpsRepository? = null,
) : AndroidViewModel(application) {

    /**
     * LAZY on purpose.
     *
     * `OpsRepository()` builds an HTTP client and reads the session.
     * Constructing it in the parameter list makes that happen the instant the
     * ViewModel is born — including when there is no session yet, and
     * including in a test that only wanted to draw the screen. An exception
     * there propagates out of the CONSTRUCTOR, and a ViewModel that fails to
     * construct takes the whole screen down instead of showing an empty list.
     */
    private val repositorio: OpsRepository by lazy { repositorioParaTeste ?: OpsRepository() }

    private val _alertas = MutableStateFlow<List<OpsAlert>>(emptyList())
    val alertas: StateFlow<List<OpsAlert>> = _alertas.asStateFlow()

    private val _vistos = MutableStateFlow(AlertasVistos.ler(application))
    val vistos: StateFlow<Set<String>> = _vistos.asStateFlow()

    private val _falhou = MutableStateFlow(false)

    /** True if the last read failed — the screen says so, never feigns silence. */
    val falhou: StateFlow<Boolean> = _falhou.asStateFlow()

    init {
        recarregar()
    }

    fun recarregar() {
        viewModelScope.launch {
            // runCatching and not a selective try/catch: NO read failure may
            // escape here. This inbox is a panel one reads — it is not worth
            // the whole app, and an unhandled exception in a ViewModel
            // coroutine brings the process down. The worst acceptable outcome
            // is the banner saying "could not read"; never an app that closes.
            val resultado = runCatching { repositorio.fetchStatus() }.getOrNull()
            when (resultado) {
                is OpsStatusResult.Success -> {
                    _alertas.value = resultado.snapshot.alerts
                    _falhou.value = false
                }
                // A repository error AND an unexpected exception land on
                // the same honest conclusion: I do not know what is firing.
                else -> _falhou.value = true
            }
        }
    }

    fun marcarVisto(alerta: OpsAlert) {
        _vistos.value = AlertasVistos.marcar(getApplication(), alerta)
    }

    fun mostrarOsVistos() {
        _vistos.value = AlertasVistos.desmarcarTudo(getApplication())
    }
}
