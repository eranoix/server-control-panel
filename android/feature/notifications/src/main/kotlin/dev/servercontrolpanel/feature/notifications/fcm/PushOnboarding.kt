package dev.servercontrolpanel.feature.notifications.fcm

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

@Composable
fun PushOnboarding() {
    val context = LocalContext.current

    val needsPermissionRequest = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
        PackageManager.PERMISSION_GRANTED

    var permissionResolved by remember { mutableStateOf(!needsPermissionRequest) }

    val permissionRequest = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) {
        permissionResolved = true
    }

    LaunchedEffect(needsPermissionRequest) {
        if (needsPermissionRequest) {
            permissionRequest.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    val batteryNotSeenYet = rememberShouldShowBatteryOptimizationPrompt()
    var showBattery by remember { mutableStateOf(batteryNotSeenYet) }
    if (permissionResolved && showBattery) {
        BatteryOptimizationPrompt(onDismissed = { showBattery = false })
    }
}
