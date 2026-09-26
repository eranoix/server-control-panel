package com.vpsmanager.feature.jira

import java.time.Duration
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter

/**
 * How long ago, in one or two words.
 *
 * ## Why relative, and not the date
 *
 * The card has room for one line, and the question that line answers is "is
 * this stalled?" — not "on what day did this move". "3 w ago" answers straight
 * away; "12/06/2026" makes the person work out the difference in their head,
 * every time, for every card in the column.
 *
 * ## Why an unreadable date becomes empty, and not an error
 *
 * Jira's timestamp comes in RFC 3339 with a colon-less offset
 * (`2026-09-09T12:00:00.000-0300`), which Java's standard formats refuse. A
 * card without the age is still a useful card; a card that does not render
 * because a date did not fit a format is invisible work.
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

/** The formats Jira sends dates in, in the order they turn up. */
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
 * The initials of whoever is assigned.
 *
 * Initials and not the photo: Jira's photos live on an Atlassian domain, and
 * fetching them from the app would mean either sending this server's
 * authentication header to a third party, or opening a proxy route in the BFF
 * just for that. The web panel uses initials when there is no photo for the
 * same reason — and on a 40 dp card an initial identifies just as well as the
 * photo.
 */
internal fun initials(name: String?): String {
    val parts = name?.trim()?.split(Regex("\\s+")).orEmpty().filter { it.isNotBlank() }
    if (parts.isEmpty()) return "?"
    val first = parts.first().first().uppercaseChar()
    if (parts.size == 1) return first.toString()
    return "$first${parts.last().first().uppercaseChar()}"
}

/**
 * The due date as it appears on the card, or empty.
 *
 * A due date already past becomes "overdue": the difference between "due in
 * 2 d" and "overdue by 2 d" is the only thing that changes what you do with the
 * card today.
 */
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
