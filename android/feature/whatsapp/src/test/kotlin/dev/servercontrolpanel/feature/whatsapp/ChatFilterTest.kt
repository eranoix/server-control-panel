package dev.servercontrolpanel.feature.whatsapp

import dev.servercontrolpanel.core.model.WhatsAppChat
import org.junit.Assert.assertEquals
import org.junit.Test

class ChatFilterTest {

    private fun chat(jid: String, group: Boolean = false, unread: Long = 0) = WhatsAppChat(
        jid = jid,
        name = jid,
        isGroup = group,
        unread = unread,
        avatarUrl = null,
        lastMessageAt = null,
        lastMessagePreview = null,
    )

    private val sample = listOf(
        chat("a", unread = 3),
        chat("b"),
        chat("g1", group = true, unread = 1),
        chat("g2", group = true),
    )

    @Test
    fun `each chip counts over the whole list, not the active filter`() {
        val c = countsByFilter(sample)

        assertEquals(4, c[ChatFilter.ALL])
        assertEquals(2, c[ChatFilter.UNREAD])
        assertEquals(2, c[ChatFilter.GROUPS])
        assertEquals(2, c[ChatFilter.PEOPLE])
    }

    @Test
    fun `groups and people are complementary, no chat is left out`() {
        val c = countsByFilter(sample)

        assertEquals(c[ChatFilter.ALL], c[ChatFilter.GROUPS]!! + c[ChatFilter.PEOPLE]!!)
    }

    @Test
    fun `filtering preserves the server order`() {
        val filtered = filterChats(sample, ChatFilter.UNREAD)

        assertEquals(listOf("a", "g1"), filtered.map { it.jid })
    }

    @Test
    fun `ALL returns the list untouched`() {
        assertEquals(sample, filterChats(sample, ChatFilter.ALL))
    }

    @Test
    fun `an empty list gives zero on every chip, never a dash`() {
        val c = countsByFilter(emptyList())

        assertEquals(ChatFilter.entries.size, c.size)
        assertEquals(setOf(0), c.values.toSet())
    }
}
