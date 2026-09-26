package com.vpsmanager.feature.auth.security

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
import com.vpsmanager.data.security.SecurityPreferences

/**
 * The door that stands BEFORE the app when the lock is on.
 *
 * ## Why it wraps the content instead of covering it
 *
 * An opaque layer on top would hide the screen, but the content beneath would
 * have been composed — and composing Home means fetching data from the
 * server. A door that loads the whole house before asking who is there is not
 * a door. Here [content] is only called once the way is clear.
 *
 * ## When it locks again
 *
 * On every `ON_STOP`. Leaving the app for any reason — another task, the
 * screen going dark, an incoming call — locks it again. A lock that only acts
 * on the first launch protects a freshly booted device and nothing else,
 * which is the least likely scenario.
 *
 * ## When it does NOT lock
 *
 * If the device has neither biometrics nor a PIN, [AppLock.available]
 * is false and the door stays open. Locking with no way to unlock would turn
 * the defence into the incident.
 */
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

    // Locks again on leaving the foreground.
    val lifecycleOwner = LocalLifecycleOwner.current
    LifecycleDisposableEffect(lifecycleOwner) { freed = false }

    if (!active || freed) {
        content()
        return
    }

    // Asks for the proof as soon as the door appears, with no tap demanded
    // first: the person opened the app, and that IS the intent to come in.
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
                // Says what is behind the door. Without this, "locked" looks
                // like an app error rather than a choice by whoever uses it.
                text = "The terminal and server access stay behind this screen.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            // The button is there for when the person cancels the system
            // prompt by mistake. Without it, the only way out would be to
            // close and reopen the app.
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

/** Observes the lifecycle and runs [onStopped] on `ON_STOP`. */
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
