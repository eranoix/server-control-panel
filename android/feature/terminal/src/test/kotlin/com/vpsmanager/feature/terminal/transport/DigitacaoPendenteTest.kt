package com.vpsmanager.feature.terminal.transport

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What these tests protect: the pending-typing strip is the only thing that
 * answers *"I press the keys and what I typed does not show up"* when the
 * connection drops. If it shows anything different from what will be sent, it
 * is worse than not existing — the person reads one command and another gets
 * executed.
 */
class DigitacaoPendenteTest {

    private fun ate(vararg pedacos: String): String {
        var acc = ""
        pedacos.forEach { acc = resumoDaDigitacao(acc, it.toByteArray(Charsets.UTF_8)) }
        return acc
    }

    @Test
    fun `texto comum aparece como foi digitado`() {
        assertEquals("ls -la", ate("l", "s", " ", "-", "l", "a"))
    }

    /**
     * DELETE REALLY DELETES. Showing "ls -laa⌫" would be showing a command the
     * person is not going to send — and they would decide based on it.
     */
    @Test
    fun `backspace remove o ultimo caractere, nao vira simbolo`() {
        val comBs = resumoDaDigitacao("ls -laa", byteArrayOf(0x08))
        assertEquals("ls -la", comBs)

        val comDel = resumoDaDigitacao("ls -laa", byteArrayOf(0x7F))
        assertEquals("ls -la", comDel)
    }

    @Test
    fun `backspace no vazio nao quebra`() {
        assertEquals("", resumoDaDigitacao("", byteArrayOf(0x08)))
    }

    /** The strip is one line; a real newline would push the grid upwards. */
    @Test
    fun `enter vira um simbolo e nao quebra a linha`() {
        val r = resumoDaDigitacao("ls", byteArrayOf(0x0D))
        assertEquals("ls⏎", r)
        assertEquals(false, r.contains('\n'))
    }

    /**
     * A `^C` in the queue changes what will happen on reconnect — it is exactly
     * the information the person needs to see before typing any more.
     */
    @Test
    fun `controle vira a forma que todo terminal ja usa`() {
        assertEquals("^C", resumoDaDigitacao("", byteArrayOf(0x03)))
        assertEquals("^D", resumoDaDigitacao("", byteArrayOf(0x04)))
    }

    /**
     * An arrow key produces `ESC [ A` — three bytes that would be garbage on
     * screen. They DO go up to the server; what does not go up to the strip is
     * their rendering.
     */
    @Test
    fun `sequencia de escape nao vira lixo na faixa`() {
        val setaCima = byteArrayOf(0x1B, '['.code.toByte(), 'A'.code.toByte())
        assertEquals("ls", resumoDaDigitacao("ls", setaCima))
    }

    @Test
    fun `escape sozinho tambem some`() {
        assertEquals("ls", resumoDaDigitacao("ls", byteArrayOf(0x1B, 'O'.code.toByte())))
    }

    /**
     * Byte by byte, a multibyte character would become one replacement
     * character per byte — "não" would appear as "nÃ£o".
     */
    @Test
    fun `acento sobrevive — UTF-8 e decodificado inteiro`() {
        assertEquals("não", ate("n", "ã", "o"))
    }

    /**
     * What matters is the END, which is where the cursor is. Trimming from the
     * front preserves what the person has just typed.
     */
    @Test
    fun `texto longo e cortado pela frente, com reticencia`() {
        val longo = "x".repeat(200)
        val r = resumoDaDigitacao("", longo.toByteArray())

        assertEquals(true, r.startsWith("…"))
        assertEquals(121, r.length)
        assertEquals(true, r.endsWith("x"))
    }
}
