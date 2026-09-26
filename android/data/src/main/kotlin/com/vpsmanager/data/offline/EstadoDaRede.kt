package com.vpsmanager.data.offline

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Whether this device has usable network right now.
 *
 * ## Why `NET_CAPABILITY_VALIDATED`, and not "an interface exists"
 *
 * The case that breaks everything is captive-portal Wi-Fi (a hotel, an
 * airport, the office Wi-Fi): the interface is connected, Android says
 * "Wi-Fi", and not one request reaches the server. `VALIDATED` is the
 * capability the system itself only grants after confirming there is a way out
 * to the internet — it is the difference between "there is a cable" and "there
 * is network".
 *
 * ## Why this does NOT decide whether a call happens
 *
 * No layer of this app asks "am I online?" before trying. Asking first is a
 * race you lose: the answer ages between the question and the `connect()`, and
 * Android 15+ cuts a background app's network without changing any capability.
 * The call is always ATTEMPTED; this state is here to **tell the truth on
 * screen** ("showing data from 11:45") and to decide when to drain what is
 * still pending.
 */
enum class EstadoDaRede {
    /** There is validated network — worth trying, worth draining the pending queue. */
    ONLINE,

    /** No validated network. The app goes on working with what it has cached. */
    OFFLINE,
}

/**
 * The process's network state, observable from any layer.
 *
 * A single `NetworkCallback` registration for the whole app: each registration
 * costs a listener in the system, and several of them scattered across the
 * modules would diverge from one another at the exact moment of the transition
 * — which is precisely the instant that matters.
 */
object RedeDoAparelho {

    private val _estado = MutableStateFlow(EstadoDaRede.ONLINE)

    /**
     * Starts at [EstadoDaRede.ONLINE] on purpose: before the system's first
     * callback, assuming offline would make the interface open saying "no
     * connection" for everyone, including for people who do have network — and
     * the error in that direction is the noisy one.
     */
    val estado: StateFlow<EstadoDaRede> = _estado.asStateFlow()

    val online: Boolean get() = _estado.value == EstadoDaRede.ONLINE

    @Volatile
    private var registrado = false

    /**
     * Registers the system observer. Idempotent — a second `onCreate` (or a
     * test) does not stack callbacks.
     */
    fun instalar(context: Context) {
        if (registrado) return
        registrado = true
        val cm = context.applicationContext
            .getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return

        // The initial state comes from the active network, not from a guess:
        // without this, an app opened already offline would take until the
        // first transition to find out.
        _estado.value = leituraAtual(cm)

        val pedido = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()

        runCatching {
            cm.registerNetworkCallback(
                pedido,
                object : ConnectivityManager.NetworkCallback() {
                    override fun onAvailable(network: Network) {
                        _estado.value = leituraAtual(cm)
                    }

                    override fun onLost(network: Network) {
                        _estado.value = leituraAtual(cm)
                    }

                    // The transition that matters most is not appear/disappear:
                    // it is the captive portal VALIDATING after the user
                    // accepts the terms. It arrives only through here.
                    override fun onCapabilitiesChanged(
                        network: Network,
                        capabilities: NetworkCapabilities,
                    ) {
                        _estado.value = leituraAtual(cm)
                    }
                },
            )
        }
    }

    private fun leituraAtual(cm: ConnectivityManager): EstadoDaRede {
        val ativa = cm.activeNetwork ?: return EstadoDaRede.OFFLINE
        val caps = cm.getNetworkCapabilities(ativa) ?: return EstadoDaRede.OFFLINE
        val vale = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        return if (vale) EstadoDaRede.ONLINE else EstadoDaRede.OFFLINE
    }

    /** Test only: returns the object to the state it had before [instalar]. */
    internal fun reiniciarParaTeste(estadoInicial: EstadoDaRede = EstadoDaRede.ONLINE) {
        registrado = false
        _estado.value = estadoInicial
    }

    /** Test only: forces a transition without going through the system. */
    internal fun definirParaTeste(novo: EstadoDaRede) {
        _estado.value = novo
    }
}
