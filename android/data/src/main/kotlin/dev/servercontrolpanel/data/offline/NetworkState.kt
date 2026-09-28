package dev.servercontrolpanel.data.offline

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

enum class NetworkState {
    ONLINE,

    OFFLINE,
}

object DeviceNetwork {

    private val _state = MutableStateFlow(NetworkState.ONLINE)

    val state: StateFlow<NetworkState> = _state.asStateFlow()

    val online: Boolean get() = _state.value == NetworkState.ONLINE

    @Volatile
    private var registered = false

    fun install(context: Context) {
        if (registered) return
        registered = true
        val cm = context.applicationContext
            .getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return

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

    internal fun resetForTest(initialState: NetworkState = NetworkState.ONLINE) {
        registered = false
        _state.value = initialState
    }

    internal fun setForTest(next: NetworkState) {
        _state.value = next
    }
}
