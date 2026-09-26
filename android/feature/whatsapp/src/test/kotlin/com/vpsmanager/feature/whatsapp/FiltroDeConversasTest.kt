package com.vpsmanager.feature.whatsapp

import com.vpsmanager.core.model.WhatsAppChat
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What these tests protect: the chip counts are what stop the screen from
 * confusing "there is nothing" with "the filter hid everything" — the most
 * expensive confusion this screen has ever produced.
 */
class FiltroDeConversasTest {

    private fun chat(jid: String, grupo: Boolean = false, naoLidas: Long = 0) = WhatsAppChat(
        jid = jid,
        name = jid,
        isGroup = grupo,
        unread = naoLidas,
        avatarUrl = null,
        lastMessageAt = null,
        lastMessagePreview = null,
    )

    private val amostra = listOf(
        chat("a", naoLidas = 3),
        chat("b"),
        chat("g1", grupo = true, naoLidas = 1),
        chat("g2", grupo = true),
    )

    /**
     * The counts are over ALL the conversations, never over the result of the
     * active filter — otherwise every unselected chip would show zero, which
     * is exactly the wrong piece of information.
     */
    @Test
    fun `cada chip conta sobre a lista inteira, nao sobre o filtro ativo`() {
        val c = contagensPorFiltro(amostra)

        assertEquals(4, c[FiltroDeConversas.TODAS])
        assertEquals(2, c[FiltroDeConversas.NAO_LIDAS])
        assertEquals(2, c[FiltroDeConversas.GRUPOS])
        assertEquals(2, c[FiltroDeConversas.PESSOAS])
    }

    @Test
    fun `grupos e pessoas sao complementares — nenhuma conversa fica de fora`() {
        val c = contagensPorFiltro(amostra)

        assertEquals(c[FiltroDeConversas.TODAS], c[FiltroDeConversas.GRUPOS]!! + c[FiltroDeConversas.PESSOAS]!!)
    }

    /** The order the server sent is the order on screen — filtering does not reorder. */
    @Test
    fun `filtrar preserva a ordem do servidor`() {
        val filtradas = filtrarConversas(amostra, FiltroDeConversas.NAO_LIDAS)

        assertEquals(listOf("a", "g1"), filtradas.map { it.jid })
    }

    @Test
    fun `TODAS devolve a lista intacta`() {
        assertEquals(amostra, filtrarConversas(amostra, FiltroDeConversas.TODAS))
    }

    /**
     * Empty inbox: every chip counts zero. It is the state in which the chips
     * and the list say the same thing, and the only one in which "0" is an
     * honest statement — the server did answer.
     */
    @Test
    fun `lista vazia da zero em todos os chips, e nunca traco`() {
        val c = contagensPorFiltro(emptyList())

        assertEquals(FiltroDeConversas.entries.size, c.size)
        assertEquals(setOf(0), c.values.toSet())
    }
}
