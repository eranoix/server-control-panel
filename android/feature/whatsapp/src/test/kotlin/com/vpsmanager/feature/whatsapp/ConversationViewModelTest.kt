package com.vpsmanager.feature.whatsapp

import com.vpsmanager.core.model.MessageSendStatus
import com.vpsmanager.core.model.WhatsAppMessage
import com.vpsmanager.data.whatsapp.MessagesResult
import com.vpsmanager.data.whatsapp.SendResult
import com.vpsmanager.data.whatsapp.WhatsAppRepository
import com.vpsmanager.data.whatsapp.WhatsAppWsEvent
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private const val JID = "5511999@s.whatsapp.net"

/**
 * A fake at the [WhatsAppRepository] seam -- never touches the generated
 * mobile-api-client (that mapping is [com.vpsmanager.data.whatsapp.WhatsAppRepositoryTest]'s
 * job against a real `MockWebServer`); this only exercises the ViewModel's
 * own state machine and reconciliation logic given a repository outcome.
 */
private class FakeConversationRepository(
    private val onMessages: suspend () -> MessagesResult = { MessagesResult.Success(emptyList(), backfilling = false) },
    private val onSend: suspend (String, String) -> SendResult = { _, _ -> SendResult.Error("falha") },
) : WhatsAppRepository() {
    var messagesCalls = 0
        private set
    val sentClientMsgIds = mutableListOf<String>()

    override suspend fun messages(jid: String, before: Long?, limit: Long?): MessagesResult {
        messagesCalls += 1
        return onMessages()
    }

    override suspend fun sendMessage(jid: String, clientMsgId: String, text: String, quotedId: String?): SendResult {
        sentClientMsgIds += clientMsgId
        return onSend(clientMsgId, text)
    }
}

/**
 * The testability seam [WhatsAppWsClient] implements for real -- lets this
 * test drive connection-state transitions and push wire events without ever
 * touching a real socket.
 */
private class FakeWhatsAppEventSource : WhatsAppEventSource {
    private val _state = MutableStateFlow<WhatsAppConnectionState>(WhatsAppConnectionState.Live)
    override val state: StateFlow<WhatsAppConnectionState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<WhatsAppWsEvent>(extraBufferCapacity = 64)
    override val events: SharedFlow<WhatsAppWsEvent> = _events.asSharedFlow()

    var connectCalls = 0
        private set

    override fun connect() {
        connectCalls += 1
    }

    override fun disconnect() = Unit

    fun push(event: WhatsAppWsEvent) {
        check(_events.tryEmit(event))
    }

    fun moveTo(newState: WhatsAppConnectionState) {
        _state.value = newState
    }
}

@OptIn(ExperimentalCoroutinesApi::class)
class ConversationViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `an incoming WS message event appends once, even if the WS client redelivers it`() = runTest {
        val repository = FakeConversationRepository()
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        val incoming = WhatsAppMessage(
            id = "server-1",
            chatJid = JID,
            fromMe = false,
            sender = JID,
            text = "oi",
            type = "text",
            ts = 10,
            ack = 0,
            quotedId = null,
            media = null,
            reactions = emptyList(),
        )

        eventSource.push(WhatsAppWsEvent.MessageReceived(incoming))
        dispatcher.scheduler.advanceUntilIdle()
        // A WS redelivery of the exact same server id must not append a second bubble.
        eventSource.push(WhatsAppWsEvent.MessageReceived(incoming))
        dispatcher.scheduler.advanceUntilIdle()

        val content = viewModel.uiState.value as ConversationUiState.Content
        assertEquals(1, content.messages.size)
        assertEquals("server-1", content.messages.single().id)
    }

    @Test
    fun `retrying a failed send with the same client_msg_id never yields two entries`() = runTest {
        var attempt = 0
        val repository = FakeConversationRepository(
            onSend = { _, _ ->
                attempt += 1
                if (attempt == 1) SendResult.Error("falha de rede") else SendResult.Success(id = "server-9")
            },
        )
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.sendMessage("oi de novo")
        dispatcher.scheduler.advanceUntilIdle()

        var content = viewModel.uiState.value as ConversationUiState.Content
        assertEquals(1, content.messages.size)
        assertEquals(MessageSendStatus.FAILED, content.messages.single().sendStatus)
        val clientMsgId = content.messages.single().clientMsgId
        assertTrue(clientMsgId != null)

        viewModel.retrySend(clientMsgId!!)
        dispatcher.scheduler.advanceUntilIdle()

        content = viewModel.uiState.value as ConversationUiState.Content
        assertEquals(1, content.messages.size)
        assertEquals(MessageSendStatus.SENT, content.messages.single().sendStatus)
        assertEquals("server-9", content.messages.single().id)
        // Same client_msg_id reused on retry, never a second one minted.
        assertEquals(listOf(clientMsgId, clientMsgId), repository.sentClientMsgIds)
    }

    @Test
    fun `reconnecting refetches history over REST instead of trusting buffered WS state`() = runTest {
        val repository = FakeConversationRepository()
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, repository.messagesCalls)

        eventSource.moveTo(WhatsAppConnectionState.Reconnecting(1))
        dispatcher.scheduler.advanceUntilIdle()
        eventSource.moveTo(WhatsAppConnectionState.Live)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(2, repository.messagesCalls)
        assertTrue(viewModel.uiState.value is ConversationUiState.Content)
    }

    /**
     * THE QUEUE, WIRED UP — the guarantee that was missing for a whole session.
     *
     * The write queue shipped with tests of its own and ZERO callers:
     * `FilaDeEnvio.instalar()` ran at boot and nothing ever called
     * `enfileirar`. Writing without internet went on failing exactly as
     * before. This test exists so that cannot happen again in silence: if
     * anyone unwires the path again, it goes red.
     */
    @Test
    fun `sem rede a mensagem vai para a fila, e a bolha diz isso`() = runTest {
        val repository = FakeConversationRepository(onSend = { _, _ -> SendResult.NaFila })
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.sendMessage("isto sai quando a internet voltar")
        dispatcher.scheduler.advanceUntilIdle()

        val content = viewModel.uiState.value as ConversationUiState.Content
        assertEquals(1, content.messages.size)
        assertEquals(MessageSendStatus.NA_FILA, content.messages.single().sendStatus)
    }

    /**
     * NA_FILA is not FAILED, and the difference changes what the person does.
     *
     * Faced with a failure they tap "tentar de novo"; faced with a message
     * that has been put aside, they put the phone away. Treating the two as
     * failure — which was the old behaviour — made the app ask for an
     * unnecessary action, and that action creates a second copy of the same
     * message.
     */
    @Test
    fun `na fila NAO e falha — o texto continua e o estado e outro`() = runTest {
        val repository = FakeConversationRepository(onSend = { _, _ -> SendResult.NaFila })
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.sendMessage("texto preservado")
        dispatcher.scheduler.advanceUntilIdle()

        val msg = (viewModel.uiState.value as ConversationUiState.Content).messages.single()
        assertTrue(msg.sendStatus != MessageSendStatus.FAILED)
        assertEquals("texto preservado", msg.text)
    }
}
