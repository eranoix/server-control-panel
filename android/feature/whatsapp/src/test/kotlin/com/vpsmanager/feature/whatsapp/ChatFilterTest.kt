package com.vpsmanager.feature.whatsapp

import com.vpsmanager.core.model.WhatsAppChat
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What these tests protect: the chip counts are what stop the screen from
 * confusing "there is nothing" with "the filter hid everything" — the most
 * expensive confusion this screen has ever produced.
 */
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

    /**
     * The counts are over ALL the conversations, never over the result of the
     * active filter — otherwise every unselected chip would show zero, which
     * is exactly the wrong piece of information.
     */
    @Test
    fun `cada chip conta sobre a lista inteira, nao sobre o filtro ativo`() {
        val c = countsByFilter(sample)

        assertEquals(4, c[ChatFilter.ALL])
        assertEquals(2, c[ChatFilter.UNREAD])
        assertEquals(2, c[ChatFilter.GROUPS])
        assertEquals(2, c[ChatFilter.PEOPLE])
    }

    @Test
    fun `grupos e pessoas sao complementares — nenhuma conversa fica de fora`() {
        val c = countsByFilter(sample)

        assertEquals(c[ChatFilter.ALL], c[ChatFilter.GROUPS]!! + c[ChatFilter.PEOPLE]!!)
    }

    /** The order the server sent is the order on screen — filtering does not reorder. */
    @Test
    fun `filtrar preserva a ordem do servidor`() {
        val filtered = filterChats(sample, ChatFilter.UNREAD)

        assertEquals(listOf("a", "g1"), filtered.map { it.jid })
    }

    @Test
    fun `TODAS devolve a lista intacta`() {
        assertEquals(sample, filterChats(sample, ChatFilter.ALL))
    }

    /**
     * Empty inbox: every chip counts zero. It is the state in which the chips
     * and the list say the same thing, and the only one in which "0" is an
     * honest statement — the server did answer.
     */
    @Test
    fun `lista vazia da zero em todos os chips, e nunca traco`() {
        val c = countsByFilter(emptyList())

        assertEquals(ChatFilter.entries.size, c.size)
        assertEquals(setOf(0), c.values.toSet())
    }
}
