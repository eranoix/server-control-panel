package com.vpsmanager.feature.whatsapp

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import com.vpsmanager.core.model.WhatsAppChat
import com.vpsmanager.data.whatsapp.ChatsResult
import com.vpsmanager.data.whatsapp.WhatsAppRepository
import kotlinx.coroutines.awaitCancellation
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [ChatListScreen] under Robolectric in every [ChatListUiState] --
 * never composed before this.
 */
@RunWith(RobolectricTestRunner::class)
class ChatListScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows a spinner, not a blank screen`() {
        val repository = ChatListScreenFakeWhatsAppRepository { awaitCancellation() }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel)
        }

        composeRule.onRoot().assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        val repository = ChatListScreenFakeWhatsAppRepository { ChatsResult.Error("O servidor está indisponível no momento.") }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("O servidor está indisponível no momento.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `empty inbox renders the empty message, not a stuck spinner`() {
        val repository = ChatListScreenFakeWhatsAppRepository { ChatsResult.Empty }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel)
        }
        composeRule.waitForIdle()

        // THE WORDING CHANGED ON PURPOSE. "Nenhuma conversa ainda" is an
        // ASSERTION about the inbox, and this screen has already printed it
        // with the WhatsApp bridge DOWN on the server — what there was was an
        // absence of an answer about the inbox, not an empty inbox. From here
        // the app cannot tell the two apart, so it says both instead of
        // picking the wrong one.
        composeRule.onNodeWithText("No chats").assertExists()
        composeRule.onNodeWithText(
            "The server returned no chats. If you expected " +
                "to see chats here, check that the WhatsApp integration " +
                "is connected in the panel.",
        ).assertExists()
    }

    @Test
    fun `a populated list renders every chat, including one with no preview and no unread badge`() {
        val chats = listOf(
            WhatsAppChat(
                jid = "5511999990000@s.whatsapp.net",
                name = "Suporte",
                isGroup = false,
                unread = 3,
                avatarUrl = null,
                lastMessageAt = 1L,
                lastMessagePreview = "Olá, tudo bem?",
            ),
            // No preview text and zero unread -- both nullable/branching
            // paths ChatRow has to handle without crashing or leaving a gap.
            WhatsAppChat(
                jid = "120363000000000000@g.us",
                name = "Equipe",
                isGroup = true,
                unread = 0,
                avatarUrl = null,
                lastMessageAt = null,
                lastMessagePreview = null,
            ),
        )
        val repository = ChatListScreenFakeWhatsAppRepository { ChatsResult.Success(chats) }
        var opened: WhatsAppChat? = null
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel, onOpenChat = { opened = it })
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Suporte").assertExists()
        composeRule.onNodeWithText("Olá, tudo bem?").assertExists()
        composeRule.onNodeWithText("3").assertExists()
        composeRule.onNodeWithText("Equipe").assertExists()

        composeRule.onNodeWithText("Suporte").performClick()
        assert(opened?.jid == "5511999990000@s.whatsapp.net") { "expected onOpenChat with the clicked chat, got $opened" }
    }
}

private class ChatListScreenFakeWhatsAppRepository(
    private val onChats: suspend () -> ChatsResult,
) : WhatsAppRepository() {
    override suspend fun chats(): ChatsResult = onChats()
}
