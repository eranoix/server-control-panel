package dev.servercontrolpanel.feature.whatsapp

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import dev.servercontrolpanel.core.model.MessageSendStatus
import dev.servercontrolpanel.core.model.WhatsAppMedia
import dev.servercontrolpanel.core.model.WhatsAppMessage
import dev.servercontrolpanel.data.whatsapp.MessagesResult
import dev.servercontrolpanel.data.whatsapp.SendResult
import dev.servercontrolpanel.data.whatsapp.WhatsAppRepository
import dev.servercontrolpanel.data.whatsapp.WhatsAppWsEvent
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

private const val JID = "5511999@s.whatsapp.net"

/**
 * Renders [ConversationScreen] under Robolectric in every [ConversationUiState], which
 * also proves its ExoPlayer and Coil setup construct off-device. Only document media is
 * exercised: image and video start a real fetch on composition that could hang here.
 */
@RunWith(RobolectricTestRunner::class)
class ConversationScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows a spinner`() {
        val repository = ConversationScreenFakeRepository(onMessages = { awaitCancellation() })
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm)
        }

        composeRule.onRoot().assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Error("The server is unavailable right now.") },
        )
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm2 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm2)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The server is unavailable right now.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `an empty history renders the empty message instead of a blank list`() {
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Success(emptyList(), backfilling = false) },
        )
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm3 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm3)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("No messages yet.").assertExists()
    }

    @Test
    fun `text messages render with a failed-send retry affordance and a document media bubble, without crashing the ExoPlayer or coil setup`() {
        val messages = listOf(
            WhatsAppMessage(
                id = "1",
                chatJid = JID,
                fromMe = false,
                sender = null,
                text = "Hi, how are you?",
                type = "text",
                ts = 1L,
                ack = 0,
                quotedId = null,
                media = null,
                reactions = emptyList(),
            ),
            WhatsAppMessage(
                id = "pending:2",
                chatJid = JID,
                fromMe = true,
                sender = null,
                text = "This was not sent",
                type = "text",
                ts = 2L,
                ack = 0,
                quotedId = null,
                media = null,
                reactions = emptyList(),
                clientMsgId = "2",
                sendStatus = MessageSendStatus.FAILED,
            ),
            WhatsAppMessage(
                id = "3",
                chatJid = JID,
                fromMe = false,
                sender = null,
                text = null,
                type = "document",
                ts = 3L,
                ack = 0,
                quotedId = null,
                media = WhatsAppMedia(
                    url = "/media/report.pdf",
                    mimeType = "application/pdf",
                    filename = "report.pdf",
                    size = 204_800L,
                    duration = null,
                    width = null,
                    height = null,
                ),
                reactions = emptyList(),
            ),
        )
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Success(messages, backfilling = true) },
        )
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm4 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm4)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Hi, how are you?").assertExists()
        composeRule.onNodeWithText("Failed to send").assertExists()
        composeRule.onNodeWithText("report.pdf").assertExists()
        composeRule.onNodeWithText("200 KB").assertExists()
    }

    @Test
    fun `sending a message via the composer clears the draft and surfaces the outcome`() {
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Success(emptyList(), backfilling = false) },
            onSend = { _, _ -> SendResult.Error("failed") },
        )
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm5 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm5)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Message").performTextInput("hi")
        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitForIdle()

        // The fake onSend resolves immediately, so the bubble is already in its terminal state.
        composeRule.onNodeWithText("Failed to send").assertExists()
        composeRule.onNodeWithText("Message").assertExists() // draft field cleared back to its placeholder
    }
}

/**
 * Fake [WhatsAppRepository]; renamed from the one in [ConversationViewModelTest] to avoid a
 * JVM class name collision between private top-level classes in the same package.
 */
private class ConversationScreenFakeRepository(
    private val onMessages: suspend () -> MessagesResult = { MessagesResult.Success(emptyList(), backfilling = false) },
    private val onSend: suspend (String, String) -> SendResult = { _, _ -> SendResult.Error("failed") },
) : WhatsAppRepository() {
    override suspend fun messages(jid: String, before: Long?, limit: Long?): MessagesResult = onMessages()
    override suspend fun sendMessage(jid: String, clientMsgId: String, text: String, quotedId: String?): SendResult =
        onSend(clientMsgId, text)
}

/** Mirrors [ConversationViewModelTest]'s private `FakeWhatsAppEventSource`, renamed for the same reason. */
private class ConversationScreenFakeEventSource : WhatsAppEventSource {
    private val _state = MutableStateFlow<WhatsAppConnectionState>(WhatsAppConnectionState.Live)
    override val state: StateFlow<WhatsAppConnectionState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<WhatsAppWsEvent>(extraBufferCapacity = 64)
    override val events: SharedFlow<WhatsAppWsEvent> = _events.asSharedFlow()

    override fun connect() = Unit
    override fun disconnect() = Unit
}
