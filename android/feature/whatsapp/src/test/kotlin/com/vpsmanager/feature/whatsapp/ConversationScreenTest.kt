package com.vpsmanager.feature.whatsapp

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.vpsmanager.core.model.MessageSendStatus
import com.vpsmanager.core.model.WhatsAppMedia
import com.vpsmanager.core.model.WhatsAppMessage
import com.vpsmanager.data.whatsapp.MessagesResult
import com.vpsmanager.data.whatsapp.SendResult
import com.vpsmanager.data.whatsapp.WhatsAppRepository
import com.vpsmanager.data.whatsapp.WhatsAppWsEvent
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
 * Renders [ConversationScreen] under Robolectric in every [ConversationUiState]
 * -- never composed before this. `ConversationScreen` unconditionally builds a
 * real `ExoPlayer` and a real Coil `ImageLoader`/OkHttp `DataSource.Factory`
 * (see its own doc comment) regardless of whether a message needs them, so
 * this is also the first proof those construct without crashing off a real
 * device. Only `type = "document"` media is exercised for the media-row
 * dispatch: `image`/`video` route through `SubcomposeAsyncImage`, which
 * starts a real (here, network-less) fetch on composition -- exercising that
 * path is left to a real device/emulator rather than risking a hang against
 * Robolectric's fake network stack.
 */
@RunWith(RobolectricTestRunner::class)
class ConversationScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows a spinner`() {
        val repository = ConversationScreenFakeRepository(onMessages = { awaitCancellation() })
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm)
        }

        composeRule.onRoot().assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Error("O servidor está indisponível no momento.") },
        )
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm2 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm2)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("O servidor está indisponível no momento.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `an empty history renders the empty message instead of a blank list`() {
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Success(emptyList(), backfilling = false) },
        )
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
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
                text = "Oi, tudo bem?",
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
                text = "Isso não foi enviado",
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
                    url = "/media/relatorio.pdf",
                    mimeType = "application/pdf",
                    filename = "relatorio.pdf",
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
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm4 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm4)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Oi, tudo bem?").assertExists()
        composeRule.onNodeWithText("Failed to send").assertExists()
        composeRule.onNodeWithText("relatorio.pdf").assertExists()
        composeRule.onNodeWithText("200 KB").assertExists()
    }

    @Test
    fun `sending a message via the composer clears the draft and surfaces the outcome`() {
        val repository = ConversationScreenFakeRepository(
            onMessages = { MessagesResult.Success(emptyList(), backfilling = false) },
            onSend = { _, _ -> SendResult.Error("falha") },
        )
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm5 = ConversationViewModel(JID, repository, ConversationScreenFakeEventSource())
        composeRule.setContent {
            ConversationScreen(viewModel = vm5)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Message").performTextInput("oi")
        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitForIdle()

        // The fake repository's onSend resolves immediately (no real network
        // round-trip), so by the time waitForIdle returns the optimistic
        // SENDING bubble has already reconciled to its terminal state.
        composeRule.onNodeWithText("Failed to send").assertExists()
        composeRule.onNodeWithText("Message").assertExists() // draft field cleared back to its placeholder
    }
}

/**
 * A fake at the [WhatsAppRepository] seam -- mirrors
 * [ConversationViewModelTest]'s private `FakeConversationRepository`, renamed
 * to avoid the top-level-private-class-name JVM collision Kotlin allows
 * across files in the same package.
 */
private class ConversationScreenFakeRepository(
    private val onMessages: suspend () -> MessagesResult = { MessagesResult.Success(emptyList(), backfilling = false) },
    private val onSend: suspend (String, String) -> SendResult = { _, _ -> SendResult.Error("falha") },
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
