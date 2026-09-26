package com.vpsmanager.data.offline

import java.io.File
import java.nio.file.Files
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * What these tests protect: the queue is the only place in the app that accepts
 * an action WITHOUT being sure it is going to happen. Every guarantee it makes
 * — ordering, persistence, tolerance of a corrupted file — has to be
 * verifiable, or "saved" becomes a word with nothing behind it.
 */
class FilaDeEnvioTest {

    private lateinit var dir: File
    private lateinit var arquivo: File

    @Before
    fun montar() {
        dir = Files.createTempDirectory("fila-de-envio").toFile()
        arquivo = File(dir, "fila.json")
        FilaDeEnvio.reiniciarParaTeste(arquivo)
    }

    @After
    fun desmontar() {
        FilaDeEnvio.reiniciarParaTeste(null)
        dir.deleteRecursively()
    }

    private fun grava(n: Int) {
        val lista = (1..n).map {
            EnvioPendente(
                id = "id-$it",
                metodo = "POST",
                caminho = "/whatsapp/chats/x/messages",
                corpoJson = """{"client_msg_id":"id-$it"}""",
                criadoEm = it.toLong(),
                descricao = "mensagem $it",
            )
        }
        arquivo.writeText(Json.encodeToString(ListSerializer(EnvioPendente.serializer()), lista))
        FilaDeEnvio.reiniciarParaTeste(arquivo)
    }

    @Test
    fun `a fila sobrevive ao processo morrer`() {
        grava(3)

        assertEquals(3, FilaDeEnvio.pendentes.value.size)
        assertEquals("id-1", FilaDeEnvio.primeiro()?.id)
    }

    @Test
    fun `a ordem e FIFO — duas acoes sobre o mesmo recurso fora de ordem dao outro estado`() {
        grava(3)

        FilaDeEnvio.concluirPrimeiro()
        assertEquals("id-2", FilaDeEnvio.primeiro()?.id)
        FilaDeEnvio.concluirPrimeiro()
        assertEquals("id-3", FilaDeEnvio.primeiro()?.id)
    }

    @Test
    fun `concluir grava no disco — reiniciar nao ressuscita o que ja saiu`() {
        grava(2)
        FilaDeEnvio.concluirPrimeiro()

        FilaDeEnvio.reiniciarParaTeste(arquivo)

        assertEquals(1, FilaDeEnvio.pendentes.value.size)
        assertEquals("id-2", FilaDeEnvio.primeiro()?.id)
    }

    /**
     * Whoever discards has to be able to SAY on screen what was lost: an action
     * that vanishes without warning is worse than one that fails in plain sight.
     */
    @Test
    fun `descartar devolve o item para quem precisa contar o que se perdeu`() {
        grava(2)

        val descartado = FilaDeEnvio.descartarPrimeiro()

        assertEquals("id-1", descartado?.id)
        assertEquals("mensagem 1", descartado?.descricao)
    }

    /**
     * A corrupted file is a real case: the process can die in the middle of a
     * write. A queue that comes up empty loses actions — opening the app with
     * an exception loses the whole app.
     */
    @Test
    fun `arquivo corrompido nao derruba o app — a fila nasce vazia`() {
        arquivo.writeText("isto não é json")

        FilaDeEnvio.reiniciarParaTeste(arquivo)

        assertTrue(FilaDeEnvio.pendentes.value.isEmpty())
    }
}

/**
 * What these tests protect: the decision to ACCEPT or REFUSE an action, and
 * what the person gets told about it afterwards.
 *
 * Neither had any coverage before — the old suite exercised only persistence,
 * with the queue seeded straight into the file. It was through a gap exactly
 * like that one that the queue shipped complete and without a single caller.
 */
class FilaDeEnvioAceiteTest {

    private lateinit var dir: File
    private lateinit var arquivo: File

    @Before
    fun montar() {
        dir = Files.createTempDirectory("fila-aceite").toFile()
        arquivo = File(dir, "fila.json")
        FilaDeEnvio.reiniciarParaTeste(arquivo)
    }

    @After
    fun desmontar() {
        FilaDeEnvio.reiniciarParaTeste(null)
        dir.deleteRecursively()
    }

    @Test
    fun `sem fila instalada a acao e recusada, nunca engolida`() {
        // The overload without a Context is the one the repositories use (they
        // do not have one). Without `instalar()` it has nowhere to write — and
        // refusing by returning `false` is what lets the caller show the usual
        // connection error. An optimistic `true` would promise an action that
        // is stored nowhere at all.
        FilaDeEnvio.reiniciarParaTeste(null)
        val aceitou = FilaDeEnvio.enfileirar(
            metodo = "POST",
            caminho = "/terminal/sessions/rename",
            corpoJson = """{"from":"dev","to":"prod"}""",
            descricao = "renomear dev para prod",
            prova = ProvaDeIdempotencia.CHAVE_NO_CABECALHO,
        )
        assertEquals(false, aceitou)
    }

    @Test
    fun `a recusa do servidor deixa de sumir em silencio`() {
        // A 4xx takes the action out of the queue for good. Before, that
        // happened with nothing showing up: the person had read "queued" and
        // would never learn it was not going to happen.
        val lista = listOf(
            EnvioPendente(
                id = "id-1",
                metodo = "POST",
                caminho = "/terminal/sessions/rename",
                corpoJson = """{"from":"dev","to":"prod"}""",
                criadoEm = 1,
                descricao = "renomear dev para prod",
            ),
        )
        arquivo.writeText(Json.encodeToString(ListSerializer(EnvioPendente.serializer()), lista))
        FilaDeEnvio.reiniciarParaTeste(arquivo)

        FilaDeEnvio.registrarRecusa()

        assertTrue("a ação tinha que sair da fila", FilaDeEnvio.pendentes.value.isEmpty())
        assertEquals(1, FilaDeEnvio.recusadas.value.size)
        assertEquals("renomear dev para prod", FilaDeEnvio.recusadas.value.first().descricao)

        FilaDeEnvio.esquecerRecusas()
        assertTrue("o aviso é para ler uma vez", FilaDeEnvio.recusadas.value.isEmpty())
    }

    @Test
    fun `a chave de idempotencia e o id do item, e ela nao muda`() {
        // This is what keeps the retry from executing twice on the other side:
        // the id is born once, is written together with the action and survives
        // closing the app. An id drawn at send time would be a new key on every
        // attempt — which is to say, no key at all.
        val lista = listOf(
            EnvioPendente(
                id = "chave-fixa",
                metodo = "POST",
                caminho = "/terminal/backups",
                corpoJson = "{}",
                criadoEm = 1,
                descricao = "backup",
            ),
        )
        arquivo.writeText(Json.encodeToString(ListSerializer(EnvioPendente.serializer()), lista))
        FilaDeEnvio.reiniciarParaTeste(arquivo)
        assertEquals("chave-fixa", FilaDeEnvio.primeiro()?.id)

        FilaDeEnvio.reiniciarParaTeste(arquivo)
        assertEquals(
            "reiniciar o app não pode trocar a chave",
            "chave-fixa",
            FilaDeEnvio.primeiro()?.id,
        )
    }
}
