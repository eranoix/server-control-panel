package com.vpsmanager.feature.notifications.prefs

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.push.NotifyPreferencesRepository
import com.vpsmanager.data.push.NotifyPreferencesResult
import com.vpsmanager.data.push.NotifyPreferencesSource
import com.vpsmanager.data.push.NotifyRule
import com.vpsmanager.data.push.UpdateNotifyPreferencesResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * State rendered by [NotificationPreferencesScreen]. [Success.errorMessage] carries a
 * transient "the last toggle failed to save" message alongside the (already-reverted) rule
 * list, rather than a separate error state — the categories themselves stay on screen while
 * an error banner surfaces above them, matching the app-wide "never blank the whole screen
 * for a partial failure" shape.
 */
sealed interface NotificationPreferencesUiState {
    data object Loading : NotificationPreferencesUiState
    data class LoadError(val message: String) : NotificationPreferencesUiState
    data class Success(val rules: List<NotifyRule>, val errorMessage: String? = null) : NotificationPreferencesUiState
}

/**
 * Drives [NotificationPreferencesScreen]: this screen is intentionally "select, don't
 * type" (toggle-per-category, no save button) — every toggle immediately issues a `PUT`
 * through [NotifyPreferencesRepository.update] scoped to [deviceId] (THIS device only,
 * never the user's other devices), reverting the toggle and surfacing an error if that
 * call fails. The server's rule catalog and `enabled_for_device` default (alert-
 * fatigue-prevention: critical-only until changed, see `notify_prefs.go`) are the single
 * source of truth — this ViewModel never hardcodes a default itself.
 */
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

    /** Toggles [ruleId] to [enabled] optimistically, then persists; reverts + surfaces an error on failure. */
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
