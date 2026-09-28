package dev.servercontrolpanel.feature.notifications.inbox

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.ops.OpsAlert
import dev.servercontrolpanel.data.ops.OpsRepository
import dev.servercontrolpanel.data.ops.OpsStatusResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

internal class AlertInboxViewModel(
    application: Application,
    repositoryForTest: OpsRepository? = null,
) : AndroidViewModel(application) {

    private val repository: OpsRepository by lazy { repositoryForTest ?: OpsRepository() }

    private val _alerts = MutableStateFlow<List<OpsAlert>>(emptyList())
    val alerts: StateFlow<List<OpsAlert>> = _alerts.asStateFlow()

    private val _seen = MutableStateFlow(SeenAlerts.read(application))
    val seen: StateFlow<Set<String>> = _seen.asStateFlow()

    private val _failed = MutableStateFlow(false)

    val failed: StateFlow<Boolean> = _failed.asStateFlow()

    init {
        reload()
    }

    fun reload() {
        viewModelScope.launch {
            val result = runCatching { repository.fetchStatus() }.getOrNull()
            when (result) {
                is OpsStatusResult.Success -> {
                    _alerts.value = result.snapshot.alerts
                    _failed.value = false
                }
                else -> _failed.value = true
            }
        }
    }

    fun markSeen(alert: OpsAlert) {
        _seen.value = SeenAlerts.mark(getApplication(), alert)
    }

    fun showSeen() {
        _seen.value = SeenAlerts.unmarkAll(getApplication())
    }
}
