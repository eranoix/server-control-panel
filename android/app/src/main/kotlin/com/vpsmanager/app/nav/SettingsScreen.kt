package com.vpsmanager.app.nav

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.designsystem.ThemeMode
import com.vpsmanager.designsystem.ThemeModeSelector

/** Test tag for the settings screen. */
internal const val TAG_SETTINGS = "tela-configuracoes"

/** Label of the manual update check. The UI and the test read it from here. */
internal const val CHECK_UPDATE_LABEL = "Check for updates"

/** Description of the manual check icon, for screen readers. */
internal const val CHECK_UPDATE_ICON_DESCRIPTION = "Check for app updates"

/**
 * Device settings.
 *
 * ## Why it exists, and what it took out of the drawer
 *
 * The appearance and the "check for updates" lived in the drawer's footer.
 * That made sense while the drawer was a list of loose screens — the
 * footer was the only "device" place there was. With the parent pages
 * there is a right place: a setting is not a work destination, and mixing
 * it with System, Docker and Operations made the drawer answer two
 * different questions.
 *
 * Signing out STAYED in the footer, and that is deliberate: ending the
 * session has to be one tap away from any screen, without navigating
 * anywhere first.
 *
 * ## The version sits next to the button
 *
 * It is the only piece of information that makes the answer verifiable.
 * Without it, "you are already on the latest version" is a claim nobody
 * can check — and the person has just spent a tap to ask.
 */
@Composable
internal fun SettingsScreen(
    themeMode: ThemeMode,
    onThemeModeChange: (ThemeMode) -> Unit,
    installedVersion: String?,
    onCheckForUpdate: () -> Unit,
    onOpenNotifications: () -> Unit,
    onOpenSecurity: () -> Unit,
    onOpenLicenses: () -> Unit,
    onOpenDiagnostics: () -> Unit,
    onOpenStorage: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(vertical = 8.dp)
            .testTag(TAG_SETTINGS),
    ) {
        // No section title here: ThemeModeSelector already announces itself as
        // "Appearance", and the two together put the same word on the screen
        // twice — the screen reader read the header and repeated it on the
        // selector.
        ThemeModeSelector(
            selected = themeMode,
            onSelect = onThemeModeChange,
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
        )

        HorizontalDivider(Modifier.padding(vertical = 12.dp))

        Heading("App")
        SettingRow(
            icon = Icons.Filled.Refresh,
            iconDescription = CHECK_UPDATE_ICON_DESCRIPTION,
            title = CHECK_UPDATE_LABEL,
            hint = installedVersion?.let { "Version $it" },
            onClick = onCheckForUpdate,
        )
        SettingRow(
            icon = NOTIFICATIONS_ICON,
            iconDescription = "Notifications",
            title = "Notifications",
            hint = "What this device receives, and when",
            onClick = onOpenNotifications,
        )
        SettingRow(
            icon = Icons.Filled.Lock,
            iconDescription = "Device security",
            title = "Device security",
            hint = "App lock, screen protection, auto-return",
            onClick = onOpenSecurity,
        )
        // It lives under "App", next to updates and notifications, because it
        // belongs to the same family: things the app does on its own and that
        // the person has a right to look at. Under "About" it would be passive
        // reading, and here there is a button that acts.
        SettingRow(
            icon = Icons.Filled.Delete,
            iconDescription = "Storage",
            title = "Storage",
            hint = "How much space the app uses, and what can be freed",
            onClick = onOpenStorage,
        )

        HorizontalDivider(Modifier.padding(vertical = 12.dp))

        Heading("About")
        SettingRow(
            icon = Icons.Filled.Warning,
            iconDescription = "Diagnostics",
            title = "Diagnostics",
            hint = "The report that explains a refused install",
            onClick = onOpenDiagnostics,
        )
        SettingRow(
            icon = Icons.Filled.Info,
            iconDescription = "Open-source licenses",
            title = "Licenses",
            hint = null,
            onClick = onOpenLicenses,
        )
    }
}

@Composable
private fun Heading(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 6.dp),
    )
}

@Composable
private fun SettingRow(
    icon: ImageVector,
    iconDescription: String,
    title: String,
    hint: String?,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 20.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = iconDescription,
            modifier = Modifier.size(22.dp),
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Column(
            modifier = Modifier.padding(start = 16.dp).fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            Text(text = title, style = MaterialTheme.typography.bodyLarge)
            hint?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
