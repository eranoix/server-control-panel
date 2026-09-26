package com.vpsmanager.feature.notifications.fcm

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat

/**
 * The post-login onboarding moment: it asks for `POST_NOTIFICATIONS` and,
 * afterwards, offers the battery exemption.
 *
 * ## Why this file exists
 *
 * **The app never asked for notification permission.** `POST_NOTIFICATIONS`
 * was declared in the manifest, and since Android 13 declaring it is not
 * enough — without the runtime request the system simply delivers nothing.
 * The only place that asked was the file browser, on starting a transfer
 * (`FileBrowserScreen`), so on a fresh install **no deploy, alert or queue
 * notification appeared at all** until the person happened to open the files
 * and send an upload. The whole notification wave was on the air without
 * reaching anywhere.
 *
 * [BatteryOptimizationPrompt] had met the same fate: written, documented as
 * "shown once, in the same post-login onboarding moment as the
 * `POST_NOTIFICATIONS` request" — and **never called from anywhere**. The
 * moment its own documentation described did not exist. This file is that
 * moment.
 *
 * ## The order matters
 *
 * The system permission first, the battery dialog afterwards. Opening one of
 * the app's own `AlertDialog`s while the system's permission sheet is on top
 * stacks two questions on the same screen — and the second arrives before the
 * person has understood the first. Only once the system sheet resolves
 * (granted or denied, either way) does the conversation about battery begin.
 *
 * Denying blocks nothing: the app carries on working, just without
 * notifications. And the request does not repeat on every launch — the system
 * stops showing the sheet after two refusals, and the battery exemption is
 * marked as seen in [com.vpsmanager.data.push.PushOnboardingState].
 */
@Composable
fun OnboardingDePush() {
    val context = LocalContext.current

    // Below Android 13 there is no runtime notification permission —
    // declaring it in the manifest is enough and the request is not possible.
    val precisaPedirPermissao = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
        PackageManager.PERMISSION_GRANTED

    // "Has the system sheet resolved yet?" — starts out true when there is
    // nothing to ask for, so that the conversation about battery is not left
    // waiting on an event that will never happen.
    var permissaoResolvida by remember { mutableStateOf(!precisaPedirPermissao) }

    val pedidoDePermissao = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) {
        // The outcome does not change what comes next: the battery exemption
        // is just as useful, and insisting on a denied permission would be
        // precisely what the prompt's own docs call "never a forced gate".
        permissaoResolvida = true
    }

    LaunchedEffect(precisaPedirPermissao) {
        if (precisaPedirPermissao) {
            pedidoDePermissao.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    val aindaNaoViuBateria = rememberShouldShowBatteryOptimizationPrompt()
    var mostrarBateria by remember { mutableStateOf(aindaNaoViuBateria) }
    if (permissaoResolvida && mostrarBateria) {
        BatteryOptimizationPrompt(onDismissed = { mostrarBateria = false })
    }
}
