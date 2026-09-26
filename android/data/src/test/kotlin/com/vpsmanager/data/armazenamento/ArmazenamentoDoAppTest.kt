package com.vpsmanager.data.armazenamento

import com.vpsmanager.data.armazenamento.ArmazenamentoDoApp.Natureza
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * A sweep that deletes what it should not is worse than no sweep at all: the
 * person loses what the network will not bring back, and finds out afterwards.
 * These tests pin exactly the boundaries the policy declares.
 */
class ArmazenamentoDoAppTest {

    @get:Rule
    val pasta = TemporaryFolder()

    private val agora = 1_700_000_000_000L
    private val umDia = 24L * 60 * 60 * 1000

    private fun arquivo(dir: File, nome: String, bytes: Int, idadeEmDias: Long): File {
        dir.mkdirs()
        val f = File(dir, nome)
        f.writeBytes(ByteArray(bytes))
        f.setLastModified(agora - idadeEmDias * umDia)
        return f
    }

    @Test
    fun `deposito auto-limitado nunca e tocado pela rotina`() {
        // The HTTP cache and media have ceilings of their own. Deleting them
        // here returns no space that was not already bounded, and costs network
        // on the next opening — on bad internet that is the opposite of upkeep.
        val cache = pasta.newFolder("bff-http")
        val antigo = arquivo(cache, "resposta", 1_000, idadeEmDias = 90)

        val r = ArmazenamentoDoApp.manutencao(
            listOf(ArmazenamentoDoApp.Deposito("Cache", "", cache, Natureza.AUTO_LIMITADO)),
            agoraMs = agora,
        )

        assertTrue("o cache se poda sozinho; a rotina nao mexe", antigo.exists())
        assertEquals(0L, r.liberadoBytes)
    }

    @Test
    fun `temporario velho sai, temporario recente fica`() {
        val tmp = pasta.newFolder("anexos")
        val velho = arquivo(tmp, "envio-abandonado", 4_000, idadeEmDias = 10)
        val recente = arquivo(tmp, "subindo-agora", 2_000, idadeEmDias = 1)

        val r = ArmazenamentoDoApp.manutencao(
            listOf(ArmazenamentoDoApp.Deposito("Temporários", "", tmp, Natureza.TEMPORARIO)),
            agoraMs = agora,
        )

        assertFalse("um envio parado ha dez dias nao vai concluir", velho.exists())
        assertTrue("um envio de ontem pode estar so esperando rede", recente.exists())
        assertEquals(4_000L, r.liberadoBytes)
        assertEquals(1, r.arquivosRemovidos)
    }

    @Test
    fun `atualizacao EM USO nunca e apagada, mesmo sendo a mais nova`() {
        // Deleting a download in flight throws away what the person already
        // paid for in network — on bad internet, the cost that hurts most.
        val staging = pasta.newFolder("atualizacoes")
        val baixando = arquivo(staging, "nova.hdiff", 9_000, idadeEmDias = 0)
        val deVersaoPassada = arquivo(staging, "antiga.apk", 50_000, idadeEmDias = 0)

        val r = ArmazenamentoDoApp.manutencao(
            listOf(ArmazenamentoDoApp.Deposito("Atualizações", "", staging, Natureza.EM_TRANSITO)),
            agoraMs = agora,
            emUso = setOf(baixando),
        )

        assertTrue("o download em andamento tem de sobreviver", baixando.exists())
        assertFalse("o artefato da versao ja instalada e lixo puro", deVersaoPassada.exists())
        assertEquals(50_000L, r.liberadoBytes)
    }

    @Test
    fun `em transito nao espera a idade - o criterio e nao estar em uso`() {
        // A rebuilt APK is tens of MB. Holding on for three days to something
        // already installed would be upkeep that keeps nothing.
        val staging = pasta.newFolder("atualizacoes")
        val instaladoHoje = arquivo(staging, "ja-instalado.apk", 60_000, idadeEmDias = 0)

        val r = ArmazenamentoDoApp.manutencao(
            listOf(ArmazenamentoDoApp.Deposito("Atualizações", "", staging, Natureza.EM_TRANSITO)),
            agoraMs = agora,
        )

        assertFalse(instaladoHoje.exists())
        assertEquals(60_000L, r.liberadoBytes)
    }

    @Test
    fun `arquivo sem data valida nao e confundido com antigo`() {
        // `lastModified` returns 0 when the filesystem does not know. Treating
        // 0 as "the year 1970, therefore old" would delete precisely what is
        // unknown — the wrong decision when the information is missing.
        val tmp = pasta.newFolder("anexos")
        val semData = arquivo(tmp, "sem-data", 1_500, idadeEmDias = 0)
        semData.setLastModified(0)

        ArmazenamentoDoApp.manutencao(
            listOf(ArmazenamentoDoApp.Deposito("Temporários", "", tmp, Natureza.TEMPORARIO)),
            agoraMs = agora,
        )

        assertTrue("sem data conhecida, nao se apaga", semData.exists())
    }

    @Test
    fun `medir soma recursivo e sobrevive a diretorio ausente`() {
        val dir = pasta.newFolder("midia")
        arquivo(File(dir, "sub"), "a", 700, idadeEmDias = 0)
        arquivo(dir, "b", 300, idadeEmDias = 0)

        val usos = ArmazenamentoDoApp.medir(
            listOf(
                ArmazenamentoDoApp.Deposito("Mídia", "", dir, Natureza.AUTO_LIMITADO),
                ArmazenamentoDoApp.Deposito("Nunca criado", "", File(dir, "inexistente"), Natureza.TEMPORARIO),
            ),
        )

        assertEquals(1_000L, usos[0].bytes)
        assertEquals("diretorio que nunca existiu e zero, nao erro", 0L, usos[1].bytes)
    }

    @Test
    fun `formatar usa a mesma base que as telas do Android`() {
        assertEquals("512 B", ArmazenamentoDoApp.formatar(512))
        assertEquals("1,5 kB", ArmazenamentoDoApp.formatar(1_500).replace('.', ','))
        assertEquals("24,0 MB", ArmazenamentoDoApp.formatar(24_000_000).replace('.', ','))
    }
}
