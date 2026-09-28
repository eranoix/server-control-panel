package dev.servercontrolpanel.feature.notifications.prefs

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.TabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import dev.servercontrolpanel.feature.notifications.inbox.AlertInbox
import dev.servercontrolpanel.feature.notifications.inbox.SeenAlerts
import dev.servercontrolpanel.feature.notifications.inbox.AlertInboxViewModel
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import dev.servercontrolpanel.data.push.DeviceIdProvider
import dev.servercontrolpanel.data.push.NotifyRule

@Composable
fun NotificationPreferencesRoute(
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val viewModel: NotificationPreferencesViewModel = viewModel(
        factory = viewModelFactory {
            initializer { NotificationPreferencesViewModel(DeviceIdProvider(context).deviceId()) }
        },
    )
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()

    val inbox: AlertInboxViewModel = viewModel(
        factory = viewModelFactory {
            initializer {
                AlertInboxViewModel(context.applicationContext as android.app.Application)
            }
        },
    )
    val alerts by inbox.alerts.collectAsStateWithLifecycle()
    val seen by inbox.seen.collectAsStateWithLifecycle()
    val inboxFailed by inbox.failed.collectAsStateWithLifecycle()

    var selectedTab by rememberSaveable { mutableStateOf(0) }
    val pending = alerts.count { SeenAlerts.keyOf(it) !in seen }

    Column(modifier = modifier.fillMaxSize()) {
        TabRow(selectedTabIndex = selectedTab) {
            Tab(
                selected = selectedTab == 0,
                onClick = { selectedTab = 0 },
                text = {
                    Text(
                        text = if (pending > 0) "Firing ($pending)" else "Firing",
                    )
                },
            )
            Tab(
                selected = selectedTab == 1,
                onClick = { selectedTab = 1 },
                text = { Text(text = "Rules") },
            )
        }
        when (selectedTab) {
            0 -> Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                AlertInbox(
                    alerts = alerts,
                    seen = seen,
                    onMarkSeen = inbox::markSeen,
                    onUnmarkAll = inbox::showSeen,
                    modifier = Modifier.padding(16.dp),
                )
                if (inboxFailed) {
                    Text(
                        text = "Could not read the alerts right now. The list above may be out of date.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(horizontal = 16.dp),
                    )
                }
            }
            else -> NotificationPreferencesScreen(
                uiState = uiState,
                onRuleToggled = viewModel::setRuleEnabled,
                onRetry = viewModel::refresh,
            )
        }
    }
}

@Composable
fun NotificationPreferencesScreen(
    uiState: NotificationPreferencesUiState,
    onRuleToggled: (ruleId: String, enabled: Boolean) -> Unit,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(modifier = modifier.fillMaxSize()) {
        when (uiState) {
            is NotificationPreferencesUiState.Loading -> {
                CircularProgressIndicator(modifier = Modifier.padding(24.dp))
            }
            is NotificationPreferencesUiState.LoadError -> {
                Column(modifier = Modifier.fillMaxWidth().padding(16.dp)) {
                    Text(text = uiState.message)
                    TextButton(onClick = onRetry) { Text(text = "Try again") }
                }
            }
            is NotificationPreferencesUiState.Success -> {
                Column(modifier = Modifier.fillMaxSize()) {
                    uiState.errorMessage?.let { message ->
                        Text(
                            text = message,
                            modifier = Modifier.fillMaxWidth().padding(16.dp),
                        )
                    }
                    LazyColumn {
                        items(uiState.rules, key = { it.id }) { rule ->
                            NotificationRuleRow(rule = rule, onToggled = { enabled -> onRuleToggled(rule.id, enabled) })
                            HorizontalDivider()
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun NotificationRuleRow(rule: NotifyRule, onToggled: (Boolean) -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(text = rule.name, modifier = Modifier.weight(1f))
        Switch(checked = rule.enabledForDevice, onCheckedChange = onToggled)
    }
}
