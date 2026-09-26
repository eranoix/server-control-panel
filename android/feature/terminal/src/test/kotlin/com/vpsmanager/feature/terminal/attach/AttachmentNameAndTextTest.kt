package com.vpsmanager.feature.terminal.attach

import java.time.Instant
import java.time.ZoneId
import java.util.UUID
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The pure logic of an attachment: how the file is NAMED on the server and
 * exactly which text ends up on the command line. No Android involved — this
 * is where the two rules live that, if wrong, cause silent damage
 * (overwriting an earlier attachment; breaking the shell argument).
 */
class AttachmentNameAndTextTest {

    private val instant = Instant.parse("2026-09-06T19:30:45Z")
    private val utc = ZoneId.of("UTC")

    @Test
    fun `nome de destino carimba data e hora para nunca sobrescrever o anexo anterior`() {
        assertEquals("20260906-193045-IMG_0001.jpg", destinationNameFor("IMG_0001.jpg", instant, utc))
    }

    @Test
    fun `duas fotos com o MESMO nome viram destinos diferentes`() {
        val first = destinationNameFor("IMG_0001.jpg", instant, utc)
        val second = destinationNameFor("IMG_0001.jpg", instant.plusSeconds(1), utc)
        // The server's `rename` overwrites silently; without distinct names,
        // the path already handed to the assistant would start pointing at
        // different content with nothing failing.
        assertTrue(first != second)
    }

    @Test
    fun `espacos no nome sao PRESERVADOS`() {
        // Spaces are handled by the shell quoting at insertion time, not by
        // mangling the name here: the file on the server should be called what
        // the operator saw in the picker.
        val name = destinationNameFor("Captura de tela.png", instant, utc)
        assertTrue(name.endsWith("-Captura de tela.png"))
    }

    @Test
    fun `separador de caminho e ponto-ponto sao removidos do nome`() {
        // InitUpload rejects the whole upload if the name tries to pick a folder.
        assertEquals("20260906-193045-passwd", destinationNameFor("../../etc/passwd", instant, utc))
        assertEquals("20260906-193045-nota.txt", destinationNameFor("C:\\Users\\a\\nota.txt", instant, utc))
    }

    @Test
    fun `nome vazio ganha um nome padrao em vez de virar so o carimbo`() {
        assertEquals("20260906-193045-anexo", destinationNameFor("   ", instant, utc))
    }

    private fun attachment(state: AttachmentState, name: String = "x") =
        ScreenAttachment(id = UUID.randomUUID(), name = name, state = state)

    @Test
    fun `so anexos prontos entram no texto inserido`() {
        val ready = attachment(AttachmentState.Ready("/srv/inbox/a.png"))
        val sending = attachment(AttachmentState.Uploading(40))
        val failed = attachment(AttachmentState.Failed("sem espaço"))

        val text = insertionTextFrom(listOf(ready, sending, failed), listOf(ready.id, sending.id, failed.id))

        assertEquals("/srv/inbox/a.png ", text)
    }

    @Test
    fun `caminho com espaco chega citado na linha de comando`() {
        val ready = attachment(AttachmentState.Ready("/srv/inbox/20260906-193045-Captura de tela.png"))

        val text = insertionTextFrom(listOf(ready), listOf(ready.id))

        assertEquals("'/srv/inbox/20260906-193045-Captura de tela.png' ", text)
    }

    @Test
    fun `varios prontos entram na ordem em que foram anexados`() {
        val a = attachment(AttachmentState.Ready("/srv/inbox/a.png"))
        val b = attachment(AttachmentState.Ready("/srv/inbox/b b.png"))

        val text = insertionTextFrom(listOf(a, b), listOf(a.id, b.id))

        assertEquals("/srv/inbox/a.png '/srv/inbox/b b.png' ", text)
    }

    @Test
    fun `sem nada pronto nao insere nada`() {
        val sending = attachment(AttachmentState.Uploading(10))
        assertEquals("", insertionTextFrom(listOf(sending), listOf(sending.id)))
    }

    @Test
    fun `percentual e desconhecido quando o provedor nao informou o tamanho`() {
        assertEquals(UNKNOWN_PERCENT, percentOf(sent = 100, total = 0))
        assertEquals(50, percentOf(sent = 50, total = 100))
        assertEquals(100, percentOf(sent = 100, total = 100))
    }

    @Test
    fun `o estado vira frase que diz o que houve, nunca um falhou generico`() {
        assertEquals("Uploading 40%", stateDescription(AttachmentState.Uploading(40)))
        assertEquals("Uploading…", stateDescription(AttachmentState.Uploading(UNKNOWN_PERCENT)))
        assertEquals("/srv/inbox/a.png", stateDescription(AttachmentState.Ready("/srv/inbox/a.png")))
        assertEquals(
            "O servidor está sem espaço em disco. Libere espaço e envie de novo.",
            stateDescription(AttachmentState.Failed("O servidor está sem espaço em disco. Libere espaço e envie de novo.")),
        )
        assertEquals("Upload canceled.", stateDescription(AttachmentState.Cancelled))
    }
}
