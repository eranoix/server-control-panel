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
class OutboxTest {

    private lateinit var dir: File
    private lateinit var file: File

    @Before
    fun build() {
        dir = Files.createTempDirectory("fila-de-envio").toFile()
        file = File(dir, "fila.json")
        Outbox.resetForTest(file)
    }

    @After
    fun tearDown() {
        Outbox.resetForTest(null)
        dir.deleteRecursively()
    }

    private fun write(n: Int) {
        val list = (1..n).map {
            PendingSend(
                id = "id-$it",
                method = "POST",
                path = "/whatsapp/chats/x/messages",
                bodyJson = """{"client_msg_id":"id-$it"}""",
                createdAt = it.toLong(),
                description = "mensagem $it",
            )
        }
        file.writeText(Json.encodeToString(ListSerializer(PendingSend.serializer()), list))
        Outbox.resetForTest(file)
    }

    @Test
    fun `a fila sobrevive ao processo morrer`() {
        write(3)

        assertEquals(3, Outbox.pending.value.size)
        assertEquals("id-1", Outbox.first()?.id)
    }

    @Test
    fun `a ordem e FIFO — duas acoes sobre o mesmo recurso fora de ordem dao outro estado`() {
        write(3)

        Outbox.completeFirst()
        assertEquals("id-2", Outbox.first()?.id)
        Outbox.completeFirst()
        assertEquals("id-3", Outbox.first()?.id)
    }

    @Test
    fun `concluir grava no disco — reiniciar nao ressuscita o que ja saiu`() {
        write(2)
        Outbox.completeFirst()

        Outbox.resetForTest(file)

        assertEquals(1, Outbox.pending.value.size)
        assertEquals("id-2", Outbox.first()?.id)
    }

    /**
     * Whoever discards has to be able to SAY on screen what was lost: an action
     * that vanishes without warning is worse than one that fails in plain sight.
     */
    @Test
    fun `descartar devolve o item para quem precisa contar o que se perdeu`() {
        write(2)

        val discarded = Outbox.dropFirst()

        assertEquals("id-1", discarded?.id)
        assertEquals("mensagem 1", discarded?.description)
    }

    /**
     * A corrupted file is a real case: the process can die in the middle of a
     * write. A queue that comes up empty loses actions — opening the app with
     * an exception loses the whole app.
     */
    @Test
    fun `arquivo corrompido nao derruba o app — a fila nasce vazia`() {
        file.writeText("isto não é json")

        Outbox.resetForTest(file)

        assertTrue(Outbox.pending.value.isEmpty())
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
class OutboxAcceptanceTest {

    private lateinit var dir: File
    private lateinit var file: File

    @Before
    fun build() {
        dir = Files.createTempDirectory("fila-aceite").toFile()
        file = File(dir, "fila.json")
        Outbox.resetForTest(file)
    }

    @After
    fun tearDown() {
        Outbox.resetForTest(null)
        dir.deleteRecursively()
    }

    @Test
    fun `sem fila instalada a acao e recusada, nunca engolida`() {
        // The overload without a Context is the one the repositories use (they
        // do not have one). Without `instalar()` it has nowhere to write — and
        // refusing by returning `false` is what lets the caller show the usual
        // connection error. An optimistic `true` would promise an action that
        // is stored nowhere at all.
        Outbox.resetForTest(null)
        val accepted = Outbox.enqueue(
            method = "POST",
            path = "/terminal/sessions/rename",
            bodyJson = """{"from":"dev","to":"prod"}""",
            description = "renomear dev para prod",
            proof = IdempotencyProof.KEY_IN_HEADER,
        )
        assertEquals(false, accepted)
    }

    @Test
    fun `a recusa do servidor deixa de sumir em silencio`() {
        // A 4xx takes the action out of the queue for good. Before, that
        // happened with nothing showing up: the person had read "queued" and
        // would never learn it was not going to happen.
        val list = listOf(
            PendingSend(
                id = "id-1",
                method = "POST",
                path = "/terminal/sessions/rename",
                bodyJson = """{"from":"dev","to":"prod"}""",
                createdAt = 1,
                description = "renomear dev para prod",
            ),
        )
        file.writeText(Json.encodeToString(ListSerializer(PendingSend.serializer()), list))
        Outbox.resetForTest(file)

        Outbox.recordRejection()

        assertTrue("a ação tinha que sair da fila", Outbox.pending.value.isEmpty())
        assertEquals(1, Outbox.rejected.value.size)
        assertEquals("renomear dev para prod", Outbox.rejected.value.first().description)

        Outbox.forgetRejections()
        assertTrue("o aviso é para ler uma vez", Outbox.rejected.value.isEmpty())
    }

    @Test
    fun `a chave de idempotencia e o id do item, e ela nao muda`() {
        // This is what keeps the retry from executing twice on the other side:
        // the id is born once, is written together with the action and survives
        // closing the app. An id drawn at send time would be a new key on every
        // attempt — which is to say, no key at all.
        val list = listOf(
            PendingSend(
                id = "chave-fixa",
                method = "POST",
                path = "/terminal/backups",
                bodyJson = "{}",
                createdAt = 1,
                description = "backup",
            ),
        )
        file.writeText(Json.encodeToString(ListSerializer(PendingSend.serializer()), list))
        Outbox.resetForTest(file)
        assertEquals("chave-fixa", Outbox.first()?.id)

        Outbox.resetForTest(file)
        assertEquals(
            "reiniciar o app não pode trocar a chave",
            "chave-fixa",
            Outbox.first()?.id,
        )
    }
}
