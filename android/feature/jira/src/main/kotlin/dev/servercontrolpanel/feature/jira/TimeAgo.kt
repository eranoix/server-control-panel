package dev.servercontrolpanel.feature.jira

import java.time.Duration
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter

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
        }
    }
    return try {
        LocalDate.parse(text).atStartOfDay().atOffset(java.time.ZoneOffset.UTC)
    } catch (e: Exception) {
        null
    }
}

internal fun initials(name: String?): String {
    val parts = name?.trim()?.split(Regex("\\s+")).orEmpty().filter { it.isNotBlank() }
    if (parts.isEmpty()) return "?"
    val first = parts.first().first().uppercaseChar()
    if (parts.size == 1) return first.toString()
    return "$first${parts.last().first().uppercaseChar()}"
}

internal fun dueLabel(timestamp: String?, today: LocalDate): String {
    val data = parseTimestamp(timestamp)?.toLocalDate() ?: return ""
    val days = java.time.temporal.ChronoUnit.DAYS.between(today, data)
    return when {
        days < 0 -> "overdue"
        days == 0L -> "due today"
        days == 1L -> "due tomorrow"
        days < 30 -> "due in $days d"
        else -> "due in ${days / 30} mo"
    }
}
