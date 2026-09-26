package com.vpsmanager.app.nav

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
import com.vpsmanager.designsystem.VpsmIcons

/** Label of the item that ends the session. The one literal — the UI and the test read it from here. */
internal const val SIGN_OUT_LABEL = "Sign out"

/** Description of the sign-out icon, for screen readers. */
internal const val SIGN_OUT_ICON_DESCRIPTION = "Sign out of the account"

/** Description of the button that opens the drawer, in the top bar. */
internal const val OPEN_DRAWER_DESCRIPTION = "Open navigation menu"

/** Labels of the two shortcuts at the top. The UI and the test read them from here. */
internal const val ATALHO_TERMINAL_LABEL = "Terminal"
internal const val ATALHO_TAREFAS_LABEL = "Tasks"

/**
 * The drawer's content: the panel's parent pages, and "Sign out" anchored.
 *
 * ## Why it shrank
 *
 * The previous version had nine loose destinations, four group headers,
 * the appearance choice and the update check — sixteen rows to navigate
 * between eight places, and even so it did not fit on an 891 dp phone.
 *
 * Now there are eight items: the web panel's seven parents (Home, System,
 * Docker, Dev, Security, Apps, Operations) plus Settings. Each parent
 * opens the grid of its children. There is no group header because there
 * is no group any more — the items ALREADY are the groups. Appearance and
 * the update check moved to Settings, which is where device tuning now
 * lives.
 *
 * ## Why "Sign out" stays outside the scroll
 *
 * That is how it vanished off the screen when a new destination arrived:
 * the whole drawer scrolled, and each addition pushed the footer a little
 * further out. Ending the session on a panel that administers a server is
 * the last thing that may depend on discovering that an area scrolls.
 *
 * ## About label clipping
 *
 * Every label is `maxLines = 1` with [TextOverflow.Clip]. It is deliberate
 * that it is NOT an ellipsis: an ellipsis hides the problem (it looks tidy
 * and unreadable), a hard clip shows up in the screenshot and in the test.
 * `AppDrawerTest` reads each label's `TextLayoutResult` and fails if any
 * of them has more than one line or overflows — so that a new name, too
 * long, breaks the build instead of arriving crooked on the operator's
 * device.
 */
@Composable
internal fun AppDrawerSheet(
    currentRoute: String?,
    onDestinationSelected: (AppDestination) -> Unit,
    onSignOut: () -> Unit,
    modifier: Modifier = Modifier,
    onAtalho: (String) -> Unit = {},
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

                // ── Shortcuts ───────────────────────────────────────────────
                //
                // The two screens you go into all day long, at ONE tap. With
                // the parent-page taxonomy, the Terminal would start costing
                // two taps (drawer → Dev → Terminal) — and the terminal is
                // the reason this app exists. The web panel does the same:
                // the Terminal lives at `dev/host`, and even so it has `g+t`
                // as a direct shortcut.
                //
                // Two, and not a row: a third shortcut would start competing
                // with the list of parents just below, and the drawer would
                // go back to having two taxonomies fighting over the same
                // height — which was the defect this reorganisation fixed.
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 12.dp, vertical = 2.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Atalho(
                        rotulo = ATALHO_TERMINAL_LABEL,
                        icone = VpsmIcons.Terminal,
                        modifier = Modifier.weight(1f),
                        onClick = { onAtalho(ROTA_TERMINAL) },
                    )
                    Atalho(
                        rotulo = ATALHO_TAREFAS_LABEL,
                        icone = VpsmIcons.Quadro,
                        modifier = Modifier.weight(1f),
                        onClick = { onAtalho(ROTA_JIRA) },
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

            // ── Footer, outside the scroll ──────────────────────────────────
            HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp))
            NavigationDrawerItem(
                icon = {
                    Icon(
                        imageVector = VpsmIcons.Logout,
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
                // Signing out is never a destination: it is never highlighted, it
                // does not navigate, and it must not look like the screen the
                // operator is on.
                selected = false,
                onClick = onSignOut,
                modifier = Modifier.padding(NavigationDrawerItemDefaults.ItemPadding),
            )
            Spacer(modifier = Modifier.height(12.dp))
        }
    }
}

/** A shortcut from the top: icon above the label, in a block wide enough for a thumb. */
@Composable
private fun Atalho(
    rotulo: String,
    icone: androidx.compose.ui.graphics.vector.ImageVector,
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
                imageVector = icone,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSecondaryContainer,
            )
            Text(
                text = rotulo,
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
                maxLines = 1,
                overflow = TextOverflow.Clip,
            )
        }
    }
}
