package dev.servercontrolpanel.feature.auth.dashboard

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

object ScrollToTopRequest {

    private val _counter = MutableStateFlow(0)

    val counter: StateFlow<Int> = _counter.asStateFlow()

    fun request() {
        _counter.value = _counter.value + 1
    }
}
