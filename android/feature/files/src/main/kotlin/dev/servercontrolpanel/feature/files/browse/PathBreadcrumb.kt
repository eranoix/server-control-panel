package dev.servercontrolpanel.feature.files.browse

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

@Composable
internal fun PathBreadcrumb(
    path: String,
    onTapSegment: (String) -> Unit,
    modifier: Modifier = Modifier,
    listState: LazyListState = rememberLazyListState(),
) {
    val crumbs = pathCrumbs(path)

    LaunchedEffect(path, crumbs.size) {
        if (crumbs.isNotEmpty()) listState.scrollToItem(crumbs.lastIndex)
    }

    LazyRow(
        state = listState,
        modifier = modifier
            .fillMaxWidth()
            .clearAndSetSemantics { contentDescription = "Current path: $path" },
        horizontalArrangement = Arrangement.spacedBy(2.dp),
        verticalAlignment = Alignment.CenterVertically,
        contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 4.dp),
    ) {
        items(items = crumbs, key = { it.path }) { crumb ->
            val current = crumb.path == path.trimEnd('/').ifEmpty { "/" }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = crumb.label,
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = if (current) FontWeight.Bold else FontWeight.Normal,
                    color = if (current) {
                        MaterialTheme.colorScheme.onSurface
                    } else {
                        MaterialTheme.colorScheme.primary
                    },
                    modifier = Modifier
                        .clip(RoundedCornerShape(6.dp))
                        .then(
                            if (current) {
                                Modifier.background(MaterialTheme.colorScheme.surfaceVariant)
                            } else {
                                Modifier.clickable { onTapSegment(crumb.path) }
                            },
                        )
                        .heightIn(min = 48.dp)
                        .wrapContentHeight(Alignment.CenterVertically)
                        .padding(horizontal = 8.dp),
                )
                if (!current) {
                    Text(
                        text = "/",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.outline,
                    )
                }
            }
        }
    }
}

internal data class PathCrumb(val label: String, val path: String)

internal fun pathCrumbs(path: String): List<PathCrumb> {
    val trimmed = path.trimEnd('/')
    val crumbs = mutableListOf(PathCrumb(label = "/", path = "/"))
    if (trimmed.isEmpty() || trimmed == "/") return crumbs
    var accumulated = ""
    trimmed.split('/').filter { it.isNotEmpty() }.forEach { part ->
        accumulated = "$accumulated/$part"
        crumbs += PathCrumb(label = part, path = accumulated)
    }
    return crumbs
}
