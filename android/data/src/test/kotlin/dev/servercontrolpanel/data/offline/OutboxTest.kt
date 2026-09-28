package dev.servercontrolpanel.data.offline

import java.io.File
import java.nio.file.Files
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class OutboxTest {

    private lateinit var dir: File
    private lateinit var file: File

    @Before
    fun build() {
        dir = Files.createTempDirectory("outbox").toFile()
        file = File(dir, "outbox.json")
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
                description = "message $it",
            )
        }
        file.writeText(Json.encodeToString(ListSerializer(PendingSend.serializer()), list))
        Outbox.resetForTest(file)
    }

    @Test
    fun `the queue survives process death`() {
        write(3)

        assertEquals(3, Outbox.pending.value.size)
        assertEquals("id-1", Outbox.first()?.id)
    }

    @Test
    fun `the order is FIFO because reordering actions on one resource changes the result`() {
        write(3)

        Outbox.completeFirst()
        assertEquals("id-2", Outbox.first()?.id)
        Outbox.completeFirst()
        assertEquals("id-3", Outbox.first()?.id)
    }

    @Test
    fun `completing persists so a restart does not resurrect sent items`() {
        write(2)
        Outbox.completeFirst()

        Outbox.resetForTest(file)

        assertEquals(1, Outbox.pending.value.size)
        assertEquals("id-2", Outbox.first()?.id)
    }

    @Test
    fun `discarding returns the item so the caller can report what was lost`() {
        write(2)

        val discarded = Outbox.dropFirst()

        assertEquals("id-1", discarded?.id)
        assertEquals("message 1", discarded?.description)
    }

    @Test
    fun `a corrupted file does not crash the app and the queue starts empty`() {
        file.writeText("this is not json")

        Outbox.resetForTest(file)

        assertTrue(Outbox.pending.value.isEmpty())
    }
}

class OutboxAcceptanceTest {

    private lateinit var dir: File
    private lateinit var file: File

    @Before
    fun build() {
        dir = Files.createTempDirectory("outbox-acceptance").toFile()
        file = File(dir, "outbox.json")
        Outbox.resetForTest(file)
    }

    @After
    fun tearDown() {
        Outbox.resetForTest(null)
        dir.deleteRecursively()
    }

    @Test
    fun `without an installed queue the action is refused, never swallowed`() {
        Outbox.resetForTest(null)
        val accepted = Outbox.enqueue(
            method = "POST",
            path = "/terminal/sessions/rename",
            bodyJson = """{"from":"dev","to":"prod"}""",
            description = "rename dev to prod",
            proof = IdempotencyProof.KEY_IN_HEADER,
        )
        assertEquals(false, accepted)
    }

    @Test
    fun `a server rejection is recorded instead of vanishing silently`() {
        val list = listOf(
            PendingSend(
                id = "id-1",
                method = "POST",
                path = "/terminal/sessions/rename",
                bodyJson = """{"from":"dev","to":"prod"}""",
                createdAt = 1,
                description = "rename dev to prod",
            ),
        )
        file.writeText(Json.encodeToString(ListSerializer(PendingSend.serializer()), list))
        Outbox.resetForTest(file)

        Outbox.recordRejection()

        assertTrue("the action must leave the queue", Outbox.pending.value.isEmpty())
        assertEquals(1, Outbox.rejected.value.size)
        assertEquals("rename dev to prod", Outbox.rejected.value.first().description)

        Outbox.forgetRejections()
        assertTrue("the notice is shown once", Outbox.rejected.value.isEmpty())
    }

    @Test
    fun `the idempotency key is the item id and it does not change`() {
        val list = listOf(
            PendingSend(
                id = "fixed-key",
                method = "POST",
                path = "/terminal/backups",
                bodyJson = "{}",
                createdAt = 1,
                description = "backup",
            ),
        )
        file.writeText(Json.encodeToString(ListSerializer(PendingSend.serializer()), list))
        Outbox.resetForTest(file)
        assertEquals("fixed-key", Outbox.first()?.id)

        Outbox.resetForTest(file)
        assertEquals(
            "restarting the app must not change the key",
            "fixed-key",
            Outbox.first()?.id,
        )
    }
}
