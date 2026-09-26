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
class AnexoNomeETextoTest {

    private val instante = Instant.parse("2026-09-06T19:30:45Z")
    private val utc = ZoneId.of("UTC")

    @Test
    fun `nome de destino carimba data e hora para nunca sobrescrever o anexo anterior`() {
        assertEquals("20260906-193045-IMG_0001.jpg", nomeDeDestinoPara("IMG_0001.jpg", instante, utc))
    }

    @Test
    fun `duas fotos com o MESMO nome viram destinos diferentes`() {
        val primeira = nomeDeDestinoPara("IMG_0001.jpg", instante, utc)
        val segunda = nomeDeDestinoPara("IMG_0001.jpg", instante.plusSeconds(1), utc)
        // The server's `rename` overwrites silently; without distinct names,
        // the path already handed to the assistant would start pointing at
        // different content with nothing failing.
        assertTrue(primeira != segunda)
    }

    @Test
    fun `espacos no nome sao PRESERVADOS`() {
        // Spaces are handled by the shell quoting at insertion time, not by
        // mangling the name here: the file on the server should be called what
        // the operator saw in the picker.
        val nome = nomeDeDestinoPara("Captura de tela.png", instante, utc)
        assertTrue(nome.endsWith("-Captura de tela.png"))
    }

    @Test
    fun `separador de caminho e ponto-ponto sao removidos do nome`() {
        // InitUpload rejects the whole upload if the name tries to pick a folder.
        assertEquals("20260906-193045-passwd", nomeDeDestinoPara("../../etc/passwd", instante, utc))
        assertEquals("20260906-193045-nota.txt", nomeDeDestinoPara("C:\\Users\\a\\nota.txt", instante, utc))
    }

    @Test
    fun `nome vazio ganha um nome padrao em vez de virar so o carimbo`() {
        assertEquals("20260906-193045-anexo", nomeDeDestinoPara("   ", instante, utc))
    }

    private fun anexo(estado: EstadoDoAnexo, nome: String = "x") =
        AnexoNaTela(id = UUID.randomUUID(), nome = nome, estado = estado)

    @Test
    fun `so anexos prontos entram no texto inserido`() {
        val pronto = anexo(EstadoDoAnexo.Pronto("/srv/inbox/a.png"))
        val enviando = anexo(EstadoDoAnexo.Enviando(40))
        val falhou = anexo(EstadoDoAnexo.Falhou("sem espaço"))

        val texto = textoParaInserirDe(listOf(pronto, enviando, falhou), listOf(pronto.id, enviando.id, falhou.id))

        assertEquals("/srv/inbox/a.png ", texto)
    }

    @Test
    fun `caminho com espaco chega citado na linha de comando`() {
        val pronto = anexo(EstadoDoAnexo.Pronto("/srv/inbox/20260906-193045-Captura de tela.png"))

        val texto = textoParaInserirDe(listOf(pronto), listOf(pronto.id))

        assertEquals("'/srv/inbox/20260906-193045-Captura de tela.png' ", texto)
    }

    @Test
    fun `varios prontos entram na ordem em que foram anexados`() {
        val a = anexo(EstadoDoAnexo.Pronto("/srv/inbox/a.png"))
        val b = anexo(EstadoDoAnexo.Pronto("/srv/inbox/b b.png"))

        val texto = textoParaInserirDe(listOf(a, b), listOf(a.id, b.id))

        assertEquals("/srv/inbox/a.png '/srv/inbox/b b.png' ", texto)
    }

    @Test
    fun `sem nada pronto nao insere nada`() {
        val enviando = anexo(EstadoDoAnexo.Enviando(10))
        assertEquals("", textoParaInserirDe(listOf(enviando), listOf(enviando.id)))
    }

    @Test
    fun `percentual e desconhecido quando o provedor nao informou o tamanho`() {
        assertEquals(PERCENTUAL_DESCONHECIDO, percentualDe(enviados = 100, total = 0))
        assertEquals(50, percentualDe(enviados = 50, total = 100))
        assertEquals(100, percentualDe(enviados = 100, total = 100))
    }

    @Test
    fun `o estado vira frase que diz o que houve, nunca um falhou generico`() {
        assertEquals("Uploading 40%", descricaoDoEstado(EstadoDoAnexo.Enviando(40)))
        assertEquals("Uploading…", descricaoDoEstado(EstadoDoAnexo.Enviando(PERCENTUAL_DESCONHECIDO)))
        assertEquals("/srv/inbox/a.png", descricaoDoEstado(EstadoDoAnexo.Pronto("/srv/inbox/a.png")))
        assertEquals(
            "O servidor está sem espaço em disco. Libere espaço e envie de novo.",
            descricaoDoEstado(EstadoDoAnexo.Falhou("O servidor está sem espaço em disco. Libere espaço e envie de novo.")),
        )
        assertEquals("Upload canceled.", descricaoDoEstado(EstadoDoAnexo.Cancelado))
    }
}
