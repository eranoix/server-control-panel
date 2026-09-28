package dev.servercontrolpanel.data.offline

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

object DataAge {

    private val _lastNetworkResponse = MutableStateFlow<Long?>(null)

    val lastNetworkResponse: StateFlow<Long?> = _lastNetworkResponse.asStateFlow()

    fun recordNetworkResponse(nowMs: Long = System.currentTimeMillis()) {
        _lastNetworkResponse.value = nowMs
    }

    internal fun resetForTest() {
        _lastNetworkResponse.value = null
    }
}
