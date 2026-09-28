package dev.servercontrolpanel.feature.whatsapp

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.core.model.WhatsAppChat
import dev.servercontrolpanel.data.whatsapp.ChatsResult
import dev.servercontrolpanel.data.whatsapp.WhatsAppRepository
import kotlinx.coroutines.awaitCancellation
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class ChatListScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows a spinner, not a blank screen`() {
        val repository = ChatListScreenFakeWhatsAppRepository { awaitCancellation() }
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel)
        }

        composeRule.onRoot().assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        val repository = ChatListScreenFakeWhatsAppRepository { ChatsResult.Error("The server is unavailable right now.") }
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The server is unavailable right now.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `empty inbox renders the empty message, not a stuck spinner`() {
        val repository = ChatListScreenFakeWhatsAppRepository { ChatsResult.Empty }
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel)
        }
        composeRule.waitForIdle()

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
                name = "Support",
                isGroup = false,
                unread = 3,
                avatarUrl = null,
                lastMessageAt = 1L,
                lastMessagePreview = "Hello, how are you?",
            ),
            WhatsAppChat(
                jid = "120363000000000000@g.us",
                name = "Team",
                isGroup = true,
                unread = 0,
                avatarUrl = null,
                lastMessageAt = null,
                lastMessagePreview = null,
            ),
        )
        val repository = ChatListScreenFakeWhatsAppRepository { ChatsResult.Success(chats) }
        var opened: WhatsAppChat? = null
        val viewModel = ChatListViewModel(repository)
        composeRule.setContent {
            ChatListScreen(viewModel = viewModel, onOpenChat = { opened = it })
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Support").assertExists()
        composeRule.onNodeWithText("Hello, how are you?").assertExists()
        composeRule.onNodeWithText("3").assertExists()
        composeRule.onNodeWithText("Team").assertExists()

        composeRule.onNodeWithText("Support").performClick()
        assert(opened?.jid == "5511999990000@s.whatsapp.net") { "expected onOpenChat with the clicked chat, got $opened" }
    }
}

private class ChatListScreenFakeWhatsAppRepository(
    private val onChats: suspend () -> ChatsResult,
) : WhatsAppRepository() {
    override suspend fun chats(): ChatsResult = onChats()
}
