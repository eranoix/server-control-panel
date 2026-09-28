package dev.servercontrolpanel.feature.auth.security

import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.compose.material3.TextButton
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.DisposableEffect
import android.provider.Settings
import android.net.Uri
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.servercontrolpanel.data.security.SecurityPreferences
import kotlinx.coroutines.launch

@Composable
fun SecurityScreen(modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val prefs = remember(context) { SecurityPreferences(context.applicationContext) }
    val scope = rememberCoroutineScope()

    val lock by prefs.lockOnOpen.collectAsStateWithLifecycle(initialValue = false)
    val capture by prefs.protectFromCapture.collectAsStateWithLifecycle(initialValue = false)

    val activity = context as? FragmentActivity
    val lockAvailable = remember(activity) {
        activity != null && AppLock.available(activity)
    }

    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        SecurityItem(
            title = "Require unlock on open",
            explanation = if (lockAvailable) {
                "Asks for your biometrics or the device PIN every time the app " +
                    "comes back to the foreground. Without it, anyone holding the phone " +
                    "unlocked has the same access to the server as you."
            } else {
                "Unavailable: this device has no biometrics or PIN set up."
            },
            checked = lock,
            enabled = lockAvailable,
            onChange = { value -> scope.launch { prefs.setLockOnOpen(value) } },
        )

        SecurityItem(
            title = "Block screenshots",
            explanation = "Prevents screenshots, screen recording and the thumbnail shown in the " +
                "recent apps list — that thumbnail is written to disk by " +
                "Android and shows the last screen, which here is usually the terminal. " +
                "In exchange, your own screenshots of this app come out black.",
            checked = capture,
            enabled = true,
            onChange = { value -> scope.launch { prefs.setProtectFromCapture(value) } },
        )

        ReturnAfterUpdate()

        Text(
            text = "Your access is already stored encrypted by the device's key vault (Android " +
                "Keystore) and never goes into backups. The options above are about whoever " +
                "has the device in hand.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun SecurityItem(
    title: String,
    explanation: String,
    checked: Boolean,
    enabled: Boolean,
    onChange: (Boolean) -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(text = title, style = MaterialTheme.typography.titleMedium)
            Text(
                text = explanation,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Switch(checked = checked, onCheckedChange = onChange, enabled = enabled)
    }
}

@Composable
private fun ReturnAfterUpdate() {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current

    var granted by remember { mutableStateOf(Settings.canDrawOverlays(context)) }
    DisposableEffect(lifecycle) {
        val observer = object : DefaultLifecycleObserver {
            override fun onResume(owner: LifecycleOwner) {
                granted = Settings.canDrawOverlays(context)
            }
        }
        lifecycle.lifecycle.addObserver(observer)
        onDispose { lifecycle.lifecycle.removeObserver(observer) }
    }

    Column(
        modifier = Modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Text(
            text = "Reopen automatically after updating",
            style = MaterialTheme.typography.titleMedium,
        )
        Text(
            text = if (granted) {
                "On. After an update the app reopens on its own, on the " +
                    "screen you were on."
            } else {
                "Right now, after an update a notification appears and you tap it to " +
                    "return. For the app to reopen on its own, Android requires the " +
                    "\"Display over other apps\" permission — it is the only way it " +
                    "allows. Without it nothing breaks: it just stays one tap."
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (!granted) {
            TextButton(
                onClick = {
                    val intent = Intent(
                        Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
                        Uri.parse("package:${context.packageName}"),
                    ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    runCatching { context.startActivity(intent) }
                },
            ) {
                Text("Open Android Settings")
            }
        }
    }
}
