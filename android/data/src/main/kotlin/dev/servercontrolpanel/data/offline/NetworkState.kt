package dev.servercontrolpanel.data.offline

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
enum class NetworkState {
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
object DeviceNetwork {

    private val _state = MutableStateFlow(NetworkState.ONLINE)

    /**
     * Starts at [NetworkState.ONLINE] on purpose: before the system's first
     * callback, assuming offline would make the interface open saying "no
     * connection" for everyone, including for people who do have network — and
     * the error in that direction is the noisy one.
     */
    val state: StateFlow<NetworkState> = _state.asStateFlow()

    val online: Boolean get() = _state.value == NetworkState.ONLINE

    @Volatile
    private var registered = false

    /**
     * Registers the system observer. Idempotent — a second `onCreate` (or a
     * test) does not stack callbacks.
     */
    fun install(context: Context) {
        if (registered) return
        registered = true
        val cm = context.applicationContext
            .getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return

        // The initial state comes from the active network, not from a guess:
        // without this, an app opened already offline would take until the
        // first transition to find out.
        _state.value = currentReading(cm)

        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()

        runCatching {
            cm.registerNetworkCallback(
                request,
                object : ConnectivityManager.NetworkCallback() {
                    override fun onAvailable(network: Network) {
                        _state.value = currentReading(cm)
                    }

                    override fun onLost(network: Network) {
                        _state.value = currentReading(cm)
                    }

                    // The transition that matters most is not appear/disappear:
                    // it is the captive portal VALIDATING after the user
                    // accepts the terms. It arrives only through here.
                    override fun onCapabilitiesChanged(
                        network: Network,
                        capabilities: NetworkCapabilities,
                    ) {
                        _state.value = currentReading(cm)
                    }
                },
            )
        }
    }

    private fun currentReading(cm: ConnectivityManager): NetworkState {
        val active = cm.activeNetwork ?: return NetworkState.OFFLINE
        val caps = cm.getNetworkCapabilities(active) ?: return NetworkState.OFFLINE
        val vale = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        return if (vale) NetworkState.ONLINE else NetworkState.OFFLINE
    }

    /** Test only: returns the object to the state it had before [install]. */
    internal fun resetForTest(initialState: NetworkState = NetworkState.ONLINE) {
        registered = false
        _state.value = initialState
    }

    /** Test only: forces a transition without going through the system. */
    internal fun setForTest(next: NetworkState) {
        _state.value = next
    }
}
