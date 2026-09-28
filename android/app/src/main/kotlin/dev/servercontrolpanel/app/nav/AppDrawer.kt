package dev.servercontrolpanel.app.nav

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.NavigationDrawerItemDefaults
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.designsystem.PanelIcons

internal const val SIGN_OUT_LABEL = "Sign out"

internal const val SIGN_OUT_ICON_DESCRIPTION = "Sign out of the account"

internal const val OPEN_DRAWER_DESCRIPTION = "Open navigation menu"

internal const val TERMINAL_SHORTCUT_LABEL = "Terminal"
internal const val TASKS_SHORTCUT_LABEL = "Tasks"

@Composable
internal fun AppDrawerSheet(
    currentRoute: String?,
    onDestinationSelected: (AppDestination) -> Unit,
    onSignOut: () -> Unit,
    modifier: Modifier = Modifier,
    onShortcut: (String) -> Unit = {},
) {
    ModalDrawerSheet(modifier = modifier) {
        Column(modifier = Modifier.fillMaxHeight()) {
            Column(
                modifier = Modifier
                    .weight(1f, fill = false)
                    .verticalScroll(rememberScrollState()),
            ) {
                Spacer(modifier = Modifier.height(8.dp))
                Text(
                    text = "Server Control Panel",
                    style = MaterialTheme.typography.titleLarge,
                    maxLines = 1,
                    overflow = TextOverflow.Clip,
                    modifier = Modifier.padding(horizontal = 28.dp, vertical = 4.dp),
                )
                HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp))

                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 12.dp, vertical = 2.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Shortcut(
                        label = TERMINAL_SHORTCUT_LABEL,
                        icon = PanelIcons.Terminal,
                        modifier = Modifier.weight(1f),
                        onClick = { onShortcut(ROUTE_TERMINAL) },
                    )
                    Shortcut(
                        label = TASKS_SHORTCUT_LABEL,
                        icon = PanelIcons.Board,
                        modifier = Modifier.weight(1f),
                        onClick = { onShortcut(ROUTE_JIRA) },
                    )
                }
                HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp))

                AppDestination.entries.forEach { destination ->
                    NavigationDrawerItem(
                        icon = {
                            Icon(
                                imageVector = destination.icon,
                                contentDescription = destination.iconDescription,
                            )
                        },
                        label = {
                            Text(
                                text = destination.label,
                                maxLines = 1,
                                overflow = TextOverflow.Clip,
                                modifier = Modifier.fillMaxWidth(),
                            )
                        },
                        selected = destination.matches(currentRoute),
                        onClick = { onDestinationSelected(destination) },
                        modifier = Modifier.padding(NavigationDrawerItemDefaults.ItemPadding),
                    )
                }
            }

            HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp))
            NavigationDrawerItem(
                icon = {
                    Icon(
                        imageVector = PanelIcons.Logout,
                        contentDescription = SIGN_OUT_ICON_DESCRIPTION,
                    )
                },
                label = {
                    Text(
                        text = SIGN_OUT_LABEL,
                        maxLines = 1,
                        overflow = TextOverflow.Clip,
                        modifier = Modifier.fillMaxWidth(),
                    )
                },
                selected = false,
                onClick = onSignOut,
                modifier = Modifier.padding(NavigationDrawerItemDefaults.ItemPadding),
            )
            Spacer(modifier = Modifier.height(12.dp))
        }
    }
}

@Composable
private fun Shortcut(
    label: String,
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    modifier: Modifier = Modifier,
    onClick: () -> Unit,
) {
    Surface(
        onClick = onClick,
        shape = RoundedCornerShape(12.dp),
        color = MaterialTheme.colorScheme.secondaryContainer,
        modifier = modifier,
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSecondaryContainer,
            )
            Text(
                text = label,
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
                maxLines = 1,
                overflow = TextOverflow.Clip,
            )
        }
    }
}
