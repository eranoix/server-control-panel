package com.vpsmanager.data.whatsapp

import com.vpsmanager.core.model.WhatsAppChat
import com.vpsmanager.mobileapiclient.api.WhatsappApi
import java.io.File
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class WhatsAppRepositoryTest {

    @get:Rule
    val tempFolder = TemporaryFolder()

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun repositoryFor(): WhatsAppRepository {
        val api = WhatsappApi(basePath = server.url("/").toString())
        return WhatsAppRepository(api)
    }

    /** A real file with enough bytes that OkHttp writes it across more than one buffer flush, so progress and a mid-transfer disconnect are both observable. */
    private fun mediaFile(sizeBytes: Int = 64 * 1024): File =
        tempFolder.newFile("attachment.jpg").apply { writeBytes(ByteArray(sizeBytes) { it.toByte() }) }

    @Test
    fun `chats maps a non-empty list to Success preserving server order`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    [
                        {"jid":"b@s.whatsapp.net","name":"Bruna","is_group":false,"unread":3,"last_message_at":1700000200,"last_message_preview":"oi"},
                        {"jid":"a@s.whatsapp.net","name":"Ana","is_group":false,"unread":0,"last_message_at":1700000100,"last_message_preview":"ok"}
                    ]
                    """.trimIndent()
                )
        )

        val result = repositoryFor().chats()

        assertEquals(
            ChatsResult.Success(
                listOf(
                    WhatsAppChat(
                        jid = "b@s.whatsapp.net",
                        name = "Bruna",
                        isGroup = false,
                        unread = 3,
                        avatarUrl = null,
                        lastMessageAt = 1700000200,
                        lastMessagePreview = "oi",
                    ),
                    WhatsAppChat(
                        jid = "a@s.whatsapp.net",
                        name = "Ana",
                        isGroup = false,
                        unread = 0,
                        avatarUrl = null,
                        lastMessageAt = 1700000100,
                        lastMessagePreview = "ok",
                    ),
                ),
            ),
            result,
        )
    }

    @Test
    fun `chats maps a zero-length list to Empty`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("[]")
        )

        val result = repositoryFor().chats()

        assertEquals(ChatsResult.Empty, result)
    }

    @Test
    fun `chats maps an HTTP error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"title":"Internal Server Error"}"""))

        val result = repositoryFor().chats()

        assertTrue(result is ChatsResult.Error)
    }

    @Test
    fun `messages maps history and the backfilling flag to Success`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    {
                        "backfilling": true,
                        "messages": [
                            {"id":"m1","chat_jid":"a@s.whatsapp.net","from_me":false,"ts":1700000000,"type":"text","text":"oi","ack":2}
                        ]
                    }
                    """.trimIndent()
                )
        )

        val result = repositoryFor().messages("a@s.whatsapp.net") as MessagesResult.Success

        assertEquals(1, result.messages.size)
        assertEquals("m1", result.messages[0].id)
        assertEquals("oi", result.messages[0].text)
        assertTrue(result.backfilling)
    }

    @Test
    fun `messages maps an HTTP error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"title":"Not Found"}"""))

        val result = repositoryFor().messages("a@s.whatsapp.net")

        assertTrue(result is MessagesResult.Error)
    }

    @Test
    fun `sendMessage maps a 200 body to Success carrying the server id`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"id":"srv-1"}""")
        )

        val result = repositoryFor().sendMessage(jid = "a@s.whatsapp.net", clientMsgId = "c1", text = "oi")

        assertEquals(SendResult.Success("srv-1"), result)
    }

    @Test
    fun `sendMessage maps a client_msg_id sent unchanged in the request body`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"id":"srv-1"}""")
        )

        repositoryFor().sendMessage(jid = "a@s.whatsapp.net", clientMsgId = "retry-abc-123", text = "oi")

        val recorded = server.takeRequest()
        assertTrue(
            "client_msg_id deve ir no corpo sem alteração",
            recorded.body.readUtf8().contains("\"client_msg_id\":\"retry-abc-123\""),
        )
    }

    @Test
    fun `sendMessage maps a network failure to Error, never throwing`() = runTest {
        // Port 1 refuses connections immediately (no listener), giving a
        // synchronous IOException without the hang a shut-down MockWebServer
        // (or an unenqueued response) would cause.
        val unreachable = WhatsAppRepository(WhatsappApi(basePath = "http://127.0.0.1:1"))

        val result = unreachable.sendMessage(jid = "a@s.whatsapp.net", clientMsgId = "c1", text = "oi")

        assertTrue(result is SendResult.Error)
    }

    @Test
    fun `uploadMedia maps a 200 body to Success carrying the server id`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"id":"srv-media-1"}""")
        )

        val result = repositoryFor().uploadMedia(jid = "a@s.whatsapp.net", clientMsgId = "m1", file = mediaFile())

        assertEquals(UploadResult.Success("srv-media-1"), result)
    }

    @Test
    fun `uploadMedia maps a 413 response to Error with overCap true`() = runTest {
        server.enqueue(MockResponse().setResponseCode(413).setBody("""{"title":"Payload Too Large"}"""))

        val result = repositoryFor().uploadMedia(jid = "a@s.whatsapp.net", clientMsgId = "m1", file = mediaFile()) as UploadResult.Error

        assertTrue(result.overCap)
    }

    @Test
    fun `uploadMedia reports increasing progress as the file is written`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"id":"srv-media-1"}""")
        )

        val reported = mutableListOf<Int>()
        repositoryFor().uploadMedia(
            jid = "a@s.whatsapp.net",
            clientMsgId = "m1",
            file = mediaFile(),
            onProgress = { reported += it },
        )

        assertTrue("onProgress deve ser chamado ao menos uma vez", reported.isNotEmpty())
        assertEquals("o último valor reportado deve ser 100%", 100, reported.last())
        assertEquals("progresso nunca pode regredir", reported, reported.sorted())
    }

    @Test
    fun `client_msg_id survives a media upload retry unchanged after a mid-transfer disconnect`() = runTest {
        // First attempt: the server drops the connection while the client is
        // still writing the multipart body -- the same failure shape a real
        // flaky mobile network produces mid-upload.
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_DURING_REQUEST_BODY))
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"id":"srv-media-1"}""")
        )

        val repository = repositoryFor()
        val file = mediaFile()

        val firstAttempt = repository.uploadMedia(jid = "a@s.whatsapp.net", clientMsgId = "retry-media-1", file = file)
        assertTrue("a primeira tentativa deve falhar (conexão derrubada)", firstAttempt is UploadResult.Error)

        val secondAttempt = repository.uploadMedia(jid = "a@s.whatsapp.net", clientMsgId = "retry-media-1", file = file)
        assertEquals(UploadResult.Success("srv-media-1"), secondAttempt)

        val firstRequestBody = server.takeRequest().body.readUtf8()
        val secondRequestBody = server.takeRequest().body.readUtf8()
        assertTrue(
            "o client_msg_id da 1ª tentativa deve ser o id gerado no cliente",
            firstRequestBody.contains("\"retry-media-1\""),
        )
        assertTrue(
            "o client_msg_id da 2ª tentativa (retry) deve ser IDÊNTICO ao da 1ª -- nunca gerar um novo id",
            secondRequestBody.contains("\"retry-media-1\""),
        )
        assertFalse(
            "o retry não pode ter gerado um client_msg_id diferente",
            secondRequestBody.contains("\"retry-media-2\""),
        )
    }
}
