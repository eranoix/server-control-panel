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
 * Fake [WhatsAppRepository] for exercising the ViewModel's state machine; the real client
 * mapping is covered by [com.vpsmanager.data.whatsapp.WhatsAppRepositoryTest].
 */
private class FakeConversationRepository(
    private val onMessages: suspend () -> MessagesResult = { MessagesResult.Success(emptyList(), backfilling = false) },
    private val onSend: suspend (String, String) -> SendResult = { _, _ -> SendResult.Error("failed") },
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

/** Fake [WhatsAppEventSource] that drives connection state and events without a socket. */
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
            text = "hi",
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
                if (attempt == 1) SendResult.Error("network failure") else SendResult.Success(id = "server-9")
            },
        )
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.sendMessage("hi again")
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

    /** Guards that sending is actually wired to the offline write queue. */
    @Test
    fun `without network the message is queued, and the bubble says so`() = runTest {
        val repository = FakeConversationRepository(onSend = { _, _ -> SendResult.Queued })
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.sendMessage("this goes out when the internet is back")
        dispatcher.scheduler.advanceUntilIdle()

        val content = viewModel.uiState.value as ConversationUiState.Content
        assertEquals(1, content.messages.size)
        assertEquals(MessageSendStatus.QUEUED, content.messages.single().sendStatus)
    }

    /**
     * QUEUED is not FAILED: showing a failure would invite a retry, and a retry
     * would create a duplicate of the queued message.
     */
    @Test
    fun `queued is NOT failed, the text stays and the state differs`() = runTest {
        val repository = FakeConversationRepository(onSend = { _, _ -> SendResult.Queued })
        val eventSource = FakeWhatsAppEventSource()
        val viewModel = ConversationViewModel(jid = JID, repository = repository, eventSource = eventSource)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.sendMessage("preserved text")
        dispatcher.scheduler.advanceUntilIdle()

        val msg = (viewModel.uiState.value as ConversationUiState.Content).messages.single()
        assertTrue(msg.sendStatus != MessageSendStatus.FAILED)
        assertEquals("preserved text", msg.text)
    }
}
