package dev.servercontrolpanel.feature.jira

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneOffset

class TimeAgoTest {

    private val now: OffsetDateTime = OffsetDateTime.of(2026, 9, 9, 12, 0, 0, 0, ZoneOffset.UTC)

    private fun hoursAgo(hours: Long) = now.minusHours(hours)
        .format(java.time.format.DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSSZ"))

    @Test
    fun `Jira's format has an offset WITHOUT a colon, and that is the one that must work`() {
        assertEquals("3 h ago", timeAgo("2026-09-09T09:00:00.000+0000", now))
    }

    @Test
    fun `the ISO format with a colon also works`() {
        assertEquals("3 h ago", timeAgo("2026-09-09T09:00:00Z", now))
    }

    @Test
    fun `the scale goes from minutes to years`() {
        assertEquals("30 min ago", timeAgo(now.minusMinutes(30).toString(), now))
        assertEquals("5 h ago", timeAgo(hoursAgo(5), now))
        assertEquals("3 d ago", timeAgo(now.minusDays(3).toString(), now))
        assertEquals("2 wk ago", timeAgo(now.minusDays(15).toString(), now))
        assertEquals("3 mo ago", timeAgo(now.minusDays(100).toString(), now))
        assertEquals("2 y ago", timeAgo(now.minusDays(800).toString(), now))
    }

    @Test
    fun `an unreadable date becomes empty, never an error`() {
        assertEquals("", timeAgo("yesterday afternoon", now))
        assertEquals("", timeAgo(null, now))
        assertEquals("", timeAgo("", now))
    }

    @Test
    fun `a future timestamp does not become a negative number`() {
        assertEquals("now", timeAgo(now.plusHours(2).toString(), now))
    }
}

class InitialsTest {

    @Test
    fun `two words give two initials`() {
        assertEquals("SR", initials("Sam Rivera"))
    }

    @Test
    fun `the middle name is skipped because the last name identifies better`() {
        assertEquals("SR", initials("Sam Lee Rivera"))
    }

    @Test
    fun `one word gives one initial`() {
        assertEquals("S", initials("sam"))
    }

    @Test
    fun `no name gives a question mark, never an empty circle`() {
        assertEquals("?", initials(null))
        assertEquals("?", initials("   "))
    }
}

class DueLabelTest {

    private val today: LocalDate = LocalDate.of(2026, 9, 9)

    @Test
    fun `overdue is distinct from due soon`() {
        assertEquals("overdue", dueLabel("2026-09-01", today))
        assertEquals("due today", dueLabel("2026-09-09", today))
        assertEquals("due tomorrow", dueLabel("2026-09-10", today))
        assertEquals("due in 5 d", dueLabel("2026-09-14", today))
    }

    @Test
    fun `without a due date, the card gets no line`() {
        assertEquals("", dueLabel(null, today))
        assertEquals("", dueLabel("never", today))
    }
}
