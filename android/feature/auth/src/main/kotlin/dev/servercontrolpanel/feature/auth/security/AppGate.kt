package dev.servercontrolpanel.feature.auth.security

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import dev.servercontrolpanel.data.security.SecurityPreferences

@Composable
fun AppGate(
    activity: FragmentActivity,
    preferences: SecurityPreferences,
    content: @Composable () -> Unit,
) {
    val requiresLock by preferences.lockOnOpen.collectAsStateWithLifecycle(initialValue = false)
    val canLock = remember(activity) { AppLock.available(activity) }
    val active = requiresLock && canLock

    var freed by remember { mutableStateOf(false) }
    var requesting by remember { mutableStateOf(false) }

    val lifecycleOwner = LocalLifecycleOwner.current
    LifecycleDisposableEffect(lifecycleOwner) { freed = false }

    if (!active || freed) {
        content()
        return
    }

    LaunchedEffect(Unit) {
        if (!requesting) {
            requesting = true
            AppLock.request(
                activity = activity,
                onUnlock = { freed = true; requesting = false },
                onGiveUp = { requesting = false },
            )
        }
    }

    Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) {
        Column(
            modifier = Modifier.fillMaxSize().padding(32.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = "App locked",
                style = MaterialTheme.typography.headlineSmall,
                textAlign = TextAlign.Center,
            )
            Text(
                text = "The terminal and server access stay behind this screen.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            Button(
                onClick = {
                    if (!requesting) {
                        requesting = true
                        AppLock.request(
                            activity = activity,
                            onUnlock = { freed = true; requesting = false },
                            onGiveUp = { requesting = false },
                        )
                    }
                },
            ) {
                Text("Unlock")
            }
        }
    }
}

@Composable
private fun LifecycleDisposableEffect(owner: LifecycleOwner, onStopped: () -> Unit) {
    androidx.compose.runtime.DisposableEffect(owner) {
        val observer = object : DefaultLifecycleObserver {
            override fun onStop(owner: LifecycleOwner) = onStopped()
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
}
