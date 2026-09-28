package dev.servercontrolpanel.feature.notifications.prefs

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.push.NotifyPreferencesRepository
import dev.servercontrolpanel.data.push.NotifyPreferencesResult
import dev.servercontrolpanel.data.push.NotifyPreferencesSource
import dev.servercontrolpanel.data.push.NotifyRule
import dev.servercontrolpanel.data.push.UpdateNotifyPreferencesResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface NotificationPreferencesUiState {
    data object Loading : NotificationPreferencesUiState
    data class LoadError(val message: String) : NotificationPreferencesUiState
    data class Success(val rules: List<NotifyRule>, val errorMessage: String? = null) : NotificationPreferencesUiState
}

class NotificationPreferencesViewModel(
    private val deviceId: String,
    private val repository: NotifyPreferencesSource = NotifyPreferencesRepository(),
) : ViewModel() {

    private val _uiState = MutableStateFlow<NotificationPreferencesUiState>(NotificationPreferencesUiState.Loading)
    val uiState: StateFlow<NotificationPreferencesUiState> = _uiState.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _uiState.value = NotificationPreferencesUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = repository.fetch(deviceId)) {
                is NotifyPreferencesResult.Success -> NotificationPreferencesUiState.Success(result.rules)
                is NotifyPreferencesResult.Error -> NotificationPreferencesUiState.LoadError(result.reason)
            }
        }
    }

    fun setRuleEnabled(ruleId: String, enabled: Boolean) {
        val current = _uiState.value
        if (current !is NotificationPreferencesUiState.Success) return
        val previousRules = current.rules
        val optimisticRules = previousRules.map { rule ->
            if (rule.id == ruleId) rule.copy(enabledForDevice = enabled) else rule
        }
        _uiState.value = NotificationPreferencesUiState.Success(optimisticRules)

        viewModelScope.launch {
            val enabledRuleIds = optimisticRules.filter { it.enabledForDevice }.map { it.id }
            when (val result = repository.update(deviceId, enabledRuleIds)) {
                is UpdateNotifyPreferencesResult.Success -> Unit
                is UpdateNotifyPreferencesResult.Error -> {
                    _uiState.value = NotificationPreferencesUiState.Success(previousRules, errorMessage = result.reason)
                }
            }
        }
    }
}
