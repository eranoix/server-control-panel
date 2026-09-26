package dev.servercontrolpanel.feature.jira

import java.time.Duration
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter

/**
 * Relative age in one or two words, since the card line answers "is this stalled?".
 *
 * Jira sends offsets without a colon (`-0300`), which Java's standard formats reject. An
 * unreadable date yields an empty string rather than an error, so the card still renders.
 */
internal fun timeAgo(timestamp: String?, now: OffsetDateTime): String {
    val whenText = parseTimestamp(timestamp) ?: return ""
    val d = Duration.between(whenText, now)
    if (d.isNegative) return "now"
    val minutes = d.toMinutes()
    return when {
        minutes < 1 -> "now"
        minutes < 60 -> "${minutes} min ago"
        minutes < 60 * 24 -> "${d.toHours()} h ago"
        d.toDays() < 7 -> "${d.toDays()} d ago"
        d.toDays() < 30 -> "${d.toDays() / 7} wk ago"
        d.toDays() < 365 -> "${d.toDays() / 30} mo ago"
        else -> "${d.toDays() / 365} y ago"
    }
}

/** Jira's date formats, most common first. */
private val FORMATS = listOf(
    DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSSZ"),
    DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ssZ"),
    DateTimeFormatter.ISO_OFFSET_DATE_TIME,
)

internal fun parseTimestamp(timestamp: String?): OffsetDateTime? {
    val text = timestamp?.trim().orEmpty()
    if (text.isEmpty()) return null
    for (f in FORMATS) {
        try {
            return OffsetDateTime.parse(text, f)
        } catch (e: Exception) {
            // Next format.
        }
    }
    // A bare date (the due-date field comes as YYYY-MM-DD).
    return try {
        LocalDate.parse(text).atStartOfDay().atOffset(java.time.ZoneOffset.UTC)
    } catch (e: Exception) {
        null
    }
}

/**
 * Assignee initials. Avatars are not fetched because they live on an Atlassian domain, which
 * would mean leaking our auth header to a third party or adding a proxy route.
 */
internal fun initials(name: String?): String {
    val parts = name?.trim()?.split(Regex("\\s+")).orEmpty().filter { it.isNotBlank() }
    if (parts.isEmpty()) return "?"
    val first = parts.first().first().uppercaseChar()
    if (parts.size == 1) return first.toString()
    return "$first${parts.last().first().uppercaseChar()}"
}

/** Due-date label for the card, or empty; a past date is shown as overdue. */
internal fun dueLabel(timestamp: String?, today: LocalDate): String {
    val data = parseTimestamp(timestamp)?.toLocalDate() ?: return ""
    val days = java.time.temporal.ChronoUnit.DAYS.between(today, data)
    return when {
        days < 0 -> "venceu"
        days == 0L -> "due today"
        days == 1L -> "due tomorrow"
        days < 30 -> "due in $days d"
        else -> "due in ${days / 30} mo"
    }
}
