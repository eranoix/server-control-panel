package dev.servercontrolpanel.data.widget

import org.junit.Assert.assertEquals
import org.junit.Test

class ServerSummaryTest {

    private val now = 1_700_000_000_000L

    @Test
    fun `with no reading the sentence says so instead of now`() {
        assertEquals("no reading yet", ageInWords(0L, now))
    }

    @Test
    fun `under a minute is now`() {
        assertEquals("just now", ageInWords(now - 30_000L, now))
    }

    @Test
    fun `minutes up to an hour`() {
        assertEquals("1 min ago", ageInWords(now - 60_000L, now))
        assertEquals("45 min ago", ageInWords(now - 45 * 60_000L, now))
    }

    @Test
    fun `half an hour, the real widget interval, shows in minutes`() {
        assertEquals("30 min ago", ageInWords(now - 30 * 60_000L, now))
    }

    @Test
    fun `hours after one hour`() {
        assertEquals("1 h ago", ageInWords(now - 60 * 60_000L, now))
        assertEquals("5 h ago", ageInWords(now - 5 * 60 * 60_000L, now))
    }

    @Test
    fun `over a day stops counting`() {
        assertEquals("over a day ago", ageInWords(now - 25L * 60 * 60_000L, now))
        assertEquals("over a day ago", ageInWords(now - 40L * 24 * 60 * 60_000L, now))
    }

    @Test
    fun `a clock going backwards does not produce a negative age`() {
        assertEquals("just now", ageInWords(now + 60_000L, now))
    }

    @Test
    fun `the empty summary shows a dash, never zero`() {
        val empty = ServerSummary.EMPTY
        assertEquals("—", empty.cpu)
        assertEquals("—", empty.memory)
        assertEquals("—", empty.disk)
        assertEquals(null, empty.alert)
    }
}
