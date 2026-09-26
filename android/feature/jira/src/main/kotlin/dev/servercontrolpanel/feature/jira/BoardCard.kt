package dev.servercontrolpanel.feature.jira

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.servercontrolpanel.data.jira.JiraCard
import java.time.LocalDate
import java.time.OffsetDateTime

/**
 * Card edge colour from the status category (`new`, `indeterminate`, `done`), which Jira
 * guarantees, unlike status names. Deliberately avoids the health colours of
 * `panelStatusColors`, so stage is never confused with urgency.
 */
@Composable
internal fun categoryColor(category: String): Color = when (category) {
    "done" -> Color(0xFF5E9E76)
    "indeterminate" -> Color(0xFF3BA9B4)
    "new" -> Color(0xFF7C8794)
    else -> MaterialTheme.colorScheme.outline
}

/**
 * Compact board card sized for about 125 dp columns: key, summary, assignee initials and age.
 * Other fields live on the issue sheet.
 *
 * The card is a single accessibility target whose description is richer than what is shown,
 * including priority, type and status.
 */
@Composable
internal fun BoardCard(
    card: JiraCard,
    modifier: Modifier = Modifier,
    selecting: Boolean = false,
    selected: Boolean = false,
    onSelect: () -> Unit = {},
    now: OffsetDateTime = OffsetDateTime.now(),
    today: LocalDate = LocalDate.now(),
) {
    val age = timeAgo(card.updated, now)
    val due = dueLabel(card.due, today)
    val band = categoryColor(card.category)

    Card(
        modifier = modifier
            .fillMaxWidth()
            .semantics { contentDescription = cardDescription(card, age, due) },
        colors = CardDefaults.cardColors(
            containerColor = if (selected) {
                MaterialTheme.colorScheme.secondaryContainer
            } else {
                MaterialTheme.colorScheme.surfaceContainerHigh
            },
        ),
        shape = RoundedCornerShape(10.dp),
    ) {
        Column(
            modifier = Modifier
                // Drawn instead of a sibling Box, which would need IntrinsicSize.Min and
                // remeasure on every drag frame.
                .drawBehind { drawRect(color = band, size = Size(3.dp.toPx(), size.height)) }
                .padding(start = 10.dp, top = 8.dp, end = 8.dp, bottom = 8.dp)
                .fillMaxWidth(),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = card.key,
                    style = MaterialTheme.typography.labelMedium,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.primary,
                    maxLines = 1,
                    overflow = TextOverflow.Clip,
                )
                Spacer(Modifier.weight(1f))
                if (selecting) {
                    // Checkbox and overdue badge never share the row; together they push the key.
                    Checkbox(
                        checked = selected,
                        onCheckedChange = { onSelect() },
                        modifier = Modifier.size(20.dp),
                    )
                } else if (due == "overdue") {
                    // Overdue is the only badge kept on the compact card.
                    Text(
                        text = "!",
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }

            Spacer(Modifier.height(4.dp))
            Text(
                text = card.summary,
                // 12sp: bodyMedium fits too few words per line at this width.
                fontSize = 12.sp,
                lineHeight = 15.sp,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 3,
                overflow = TextOverflow.Ellipsis,
            )

            Spacer(Modifier.height(6.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .size(18.dp)
                        .clip(CircleShape)
                        .background(
                            if (card.assignee == null) {
                                MaterialTheme.colorScheme.surfaceVariant
                            } else {
                                MaterialTheme.colorScheme.primaryContainer
                            },
                        ),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = if (card.assignee == null) "–" else initials(card.assignee),
                        fontSize = 9.sp,
                        color = if (card.assignee == null) {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        } else {
                            MaterialTheme.colorScheme.onPrimaryContainer
                        },
                    )
                }
                Spacer(Modifier.width(6.dp))
                Text(
                    text = age,
                    fontSize = 10.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Clip,
                )
            }
        }
    }
}

/** Screen reader description of a card, including the details the compact card hides. */
internal fun cardDescription(card: JiraCard, age: String, due: String): String = buildString {
    append(card.key)
    append(", ")
    append(card.summary)
    append(". Status: ")
    append(card.status)
    card.type?.takeIf { it.isNotBlank() }?.let { append(". Type: $it") }
    card.priority?.takeIf { it.isNotBlank() }?.let { append(". Priority: $it") }
    if (card.labels.isNotEmpty()) append(". Labels: ${card.labels.joinToString(", ")}")
    append(". ")
    append(card.assignee?.let { "Assignee: $it" } ?: "Unassigned")
    if (age.isNotEmpty()) append(". Updated $age")
    if (due.isNotEmpty()) append(". $due")
}
