package dev.servercontrolpanel.feature.admin

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.data.sdui.SduiSection

/** Label of the search field, shared by the UI and its test. */
internal const val ADMIN_SEARCH_LABEL = "Search sections"

/** Header of the recents strip. */
internal const val ADMIN_RECENTS_LABEL = "Recent"

/**
 * The Administration launcher: a search field on top filtering a grid of sections.
 *
 * The grid serves recognition and search serves recall; with this many sections
 * neither alone is enough. Notification deep links bypass the launcher and open
 * their section directly. "No results" is its own state ([NoResults]).
 *
 * Labels, groups and order come from `GET /screens`, already RBAC-filtered by the server.
 */
@Composable
internal fun AdminLauncher(
    sections: List<SduiSection>,
    query: String,
    recents: List<SduiSection>,
    onQueryChange: (String) -> Unit,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val filtered = filterSections(sections, query)
    val searching = query.isNotBlank()

    Column(modifier = modifier.fillMaxSize()) {
        OutlinedTextField(
            value = query,
            onValueChange = onQueryChange,
            singleLine = true,
            label = { Text(ADMIN_SEARCH_LABEL) },
            leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
            trailingIcon = {
                if (searching) {
                    IconButton(onClick = { onQueryChange("") }) {
                        Icon(Icons.Filled.Close, contentDescription = "Clear search")
                    }
                }
            },
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 8.dp),
        )

        if (filtered.isEmpty()) {
            NoResults(term = query, total = sections.size, onClear = { onQueryChange("") })
            return@Column
        }

        LazyVerticalGrid(
            // Adaptive for phones and tablets; 112dp still fits a two-word label on two lines.
            columns = GridCells.Adaptive(minSize = 112.dp),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(
                start = 12.dp, end = 12.dp, bottom = 24.dp,
            ),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.fillMaxSize(),
        ) {
            // Recents are hidden while searching so they do not mix with results.
            if (!searching && recents.isNotEmpty()) {
                item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                    GroupHeader(ADMIN_RECENTS_LABEL)
                }
                items(recents, key = { "recent-${it.id}" }) { section ->
                    SectionCard(section = section, onClick = { onSelect(section.id) })
                }
                item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                    GroupHeader("All sections")
                }
            }

            items(filtered, key = { it.id }) { section ->
                SectionCard(section = section, onClick = { onSelect(section.id) })
            }
        }
    }
}

/**
 * Filters by label, group and id (the id matters when coming from a log or error message).
 */
internal fun filterSections(sections: List<SduiSection>, query: String): List<SduiSection> {
    val term = query.trim()
    if (term.isEmpty()) return sections
    return sections.filter { section ->
        section.label.contains(term, ignoreCase = true) ||
            section.group.contains(term, ignoreCase = true) ||
            section.id.contains(term, ignoreCase = true)
    }
}

@Composable
private fun GroupHeader(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 4.dp, top = 12.dp, bottom = 2.dp),
    )
}

/**
 * A section tile: icon ([sectionIcon]) tinted with its group color ([groupColor]),
 * short label and group name.
 */
@Composable
private fun SectionCard(section: SduiSection, onClick: () -> Unit) {
    val color = groupColor(section.group)
    Card(
        onClick = onClick,
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(
            // Neutral background; the group color is only an accent.
            containerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
        ),
        // 128dp fits a two-line label plus the group line.
        modifier = Modifier.height(128.dp),
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 8.dp, vertical = 10.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Surface(
                shape = CircleShape,
                // 18% alpha: visible in both themes without competing with the icon.
                color = color.copy(alpha = 0.18f),
                modifier = Modifier.size(40.dp),
            ) {
                Box(contentAlignment = Alignment.Center) {
                    // No content description: the label below is on the same
                    // tap target, so a screen reader would repeat it.
                    Icon(
                        imageVector = sectionIcon(section.id),
                        contentDescription = null,
                        tint = color,
                        modifier = Modifier.size(22.dp),
                    )
                }
            }
            Text(
                text = shortLabel(section.label),
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 6.dp),
            )
            // The group name in its color acts as the legend for that color.
            Text(
                text = section.group,
                style = MaterialTheme.typography.labelSmall,
                color = color,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * No search results. Shows the term and the total section count so it is not
 * mistaken for an empty catalog.
 */
@Composable
private fun NoResults(term: String, total: Int, onClear: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Icon(
            imageVector = Icons.Filled.Search,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(40.dp),
        )
        Text(
            text = "Nothing matches “$term”",
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(top = 12.dp),
        )
        Text(
            text = "None of the $total sections match that text.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 4.dp),
        )
        Text(
            text = "Clear search",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier
                .padding(top = 16.dp)
                .clickable(onClick = onClear)
                .padding(8.dp),
        )
    }
}

/**
 * Shown when the server offers this user no sections at all (no admin
 * permission). Not an error, and distinct from an empty search: there is no
 * search field here.
 */
@Composable
internal fun AdminCatalogEmpty(modifier: Modifier = Modifier) {
    Column(modifier = modifier.fillMaxWidth().padding(24.dp)) {
        Text(
            text = "No sections available",
            style = MaterialTheme.typography.titleMedium,
        )
        Text(
            text = "Your account does not have permission for any of the admin sections " +
                "on this server. Ask an administrator to grant access to what you need.",
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.padding(top = 8.dp),
        )
    }
}
