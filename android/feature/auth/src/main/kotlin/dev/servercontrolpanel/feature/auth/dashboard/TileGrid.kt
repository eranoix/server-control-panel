package dev.servercontrolpanel.feature.auth.dashboard

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.data.dashboard.Severity
import dev.servercontrolpanel.designsystem.panelStatusColors

/**
 * The dashboard grid of chosen tiles.
 *
 * Fixed two columns (not adaptive) so numbers stay readable at arm's length;
 * wide tiles take a full row.
 *
 * Not a lazy grid: it sits inside Home's `LazyColumn`, where a nested lazy grid
 * gets infinite height and crashes. With at most [ChosenTiles.MAX] tiles,
 * composing them all is cheap.
 */
@Composable
internal fun TileGrid(
    tiles: List<DashboardTile>,
    editing: Boolean,
    onTap: (DashboardTile) -> Unit,
    onRemove: (DashboardTile) -> Unit,
    onRequestCatalog: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        gridRows(tiles).forEach { line ->
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                line.forEach { tile ->
                    Box(modifier = Modifier.weight(if (tile.wide) 2f else 1f)) {
                        Tile(
                            tile = tile,
                            editing = editing,
                            onTap = { onTap(tile) },
                            onRemove = { onRemove(tile) },
                        )
                    }
                }
                // Pad a lone narrow tile so it does not stretch to full width.
                if (line.size == 1 && !line.first().wide) {
                    Box(modifier = Modifier.weight(1f))
                }
            }
        }
        if (editing) AddTile(onRequestCatalog)
    }
}

/**
 * Splits tiles into rows of two. A wide tile closes the current row and takes
 * its own, keeping columns aligned.
 */
internal fun gridRows(tiles: List<DashboardTile>): List<List<DashboardTile>> {
    val lines = mutableListOf<List<DashboardTile>>()
    var current = mutableListOf<DashboardTile>()
    fun close() {
        if (current.isNotEmpty()) {
            lines += current.toList()
            current = mutableListOf()
        }
    }
    tiles.forEach { tile ->
        if (tile.wide) {
            close()
            lines += listOf(tile)
        } else {
            current += tile
            if (current.size == 2) close()
        }
    }
    close()
    return lines
}

/**
 * One tile, colored with the shared `panelStatusColors` (never a local palette).
 */
@Composable
private fun Tile(
    tile: DashboardTile,
    editing: Boolean,
    onTap: () -> Unit,
    onRemove: () -> Unit,
) {
    val statusColors = panelStatusColors
    // Use container and content from the same pair; mixing in other theme
    // colors breaks the guaranteed contrast.
    val par = when (tile.severity) {
        Severity.OK -> statusColors.ok
        Severity.WARNING -> statusColors.warning
        Severity.CRITICAL -> statusColors.critical
    }
    val background = par.container
    val ink = par.content
    Card(
        colors = CardDefaults.cardColors(containerColor = background, contentColor = ink),
        modifier = Modifier
            .fillMaxWidth()
            // Minimum, not fixed, height so two-line text can grow.
            .heightIn(min = 92.dp)
            .clickable(enabled = !editing, onClick = onTap),
    ) {
        Column(modifier = Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                // No content description: the label beside it is what gets announced.
                Icon(
                    imageVector = tileIcon(tile.id),
                    contentDescription = null,
                    tint = ink,
                    modifier = Modifier.size(14.dp).padding(end = 0.dp),
                )
                Spacer(modifier = Modifier.width(5.dp))
                Text(
                    text = tile.label.uppercase(),
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (editing) {
                    // A word, not an X, to make clear it removes the tile, not the resource.
                    TextButton(onClick = onRemove, contentPadding = androidx.compose.foundation.layout.PaddingValues(4.dp)) {
                        Text(text = "Remove", style = MaterialTheme.typography.labelSmall)
                    }
                }
            }
            Text(
                text = tile.value,
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (tile.sub.isNotBlank()) {
                Text(
                    text = tile.sub,
                    style = MaterialTheme.typography.bodySmall,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun AddTile(onRequestCatalog: () -> Unit) {
    OutlinedCard(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 56.dp)
            .clickable(onClick = onRequestCatalog),
    ) {
        Box(modifier = Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
            Text(text = "+  Add tile", style = MaterialTheme.typography.labelLarge)
        }
    }
}

/**
 * The tile catalog from the server. Tiles already on the dashboard stay listed
 * but disabled, so items never seem to vanish.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun TileCatalog(
    catalog: List<DashboardTile>,
    chosen: List<String>,
    atCap: Boolean,
    onChoose: (DashboardTile) -> Unit,
    onClose: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onClose) {
        Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) {
            Text(text = "Available tiles", style = MaterialTheme.typography.titleMedium)
            Text(
                text = if (atCap) {
                    "The dashboard is at its limit of ${ChosenTiles.MAX} tiles. " +
                        "Remove one to add another."
                } else {
                    "The list comes from the server — a new disk shows up here on its own."
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(bottom = 8.dp),
            )
        }
        LazyVerticalGrid(
            columns = GridCells.Fixed(2),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.heightIn(max = 380.dp),
        ) {
            items(items = catalog, key = { it.id }) { tile ->
                val alreadyThere = tile.id in chosen
                OutlinedCard(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable(enabled = !alreadyThere && !atCap) { onChoose(tile) },
                ) {
                    Column(modifier = Modifier.padding(10.dp)) {
                        Text(
                            text = tile.label,
                            style = MaterialTheme.typography.labelLarge,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                            color = if (alreadyThere) {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            } else {
                                MaterialTheme.colorScheme.onSurface
                            },
                        )
                        Text(
                            text = if (alreadyThere) "already on the dashboard" else tile.value,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}
