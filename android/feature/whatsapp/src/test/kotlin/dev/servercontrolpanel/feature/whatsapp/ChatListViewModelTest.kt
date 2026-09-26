package dev.servercontrolpanel.feature.whatsapp

import dev.servercontrolpanel.core.model.WhatsAppChat
import dev.servercontrolpanel.data.whatsapp.ChatsResult
import dev.servercontrolpanel.data.whatsapp.WhatsAppRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test

/** Fake [WhatsAppRepository] for exercising the ViewModel's state machine. */
private class FakeWhatsAppRepository(
    private val onChats: suspend () -> ChatsResult = { ChatsResult.Empty },
) : WhatsAppRepository() {
    override suspend fun chats(): ChatsResult = onChats()
}

@OptIn(ExperimentalCoroutinesApi::class)
class ChatListViewModelTest {

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
    fun `starts Loading, then reaches Success with the chats in server order, unmodified`() = runTest {
        val chats = listOf(
            WhatsAppChat(
                jid = "5511999@s.whatsapp.net",
                name = "Zeca",
                isGroup = false,
                unread = 2,
                avatarUrl = null,
                lastMessageAt = 100,
                lastMessagePreview = "hi",
            ),
            WhatsAppChat(
                jid = "5511888@s.whatsapp.net",
                name = "Ana",
                isGroup = false,
                unread = 0,
                avatarUrl = "https://example/a.jpg",
                lastMessageAt = 50,
                lastMessagePreview = "bye",
            ),
        )
        val repository = FakeWhatsAppRepository(onChats = { ChatsResult.Success(chats) })
        val viewModel = ChatListViewModel(repository)

        assertEquals(ChatListUiState.Loading, viewModel.uiState.value)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(ChatListUiState.Success(chats), viewModel.uiState.value)
    }

    @Test
    fun `reaches Empty when the repository reports zero chats`() = runTest {
        val repository = FakeWhatsAppRepository(onChats = { ChatsResult.Empty })
        val viewModel = ChatListViewModel(repository)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(ChatListUiState.Empty, viewModel.uiState.value)
    }

    @Test
    fun `reaches Error when the repository reports a failure`() = runTest {
        val repository = FakeWhatsAppRepository(onChats = { ChatsResult.Error("The server is unavailable right now.") })
        val viewModel = ChatListViewModel(repository)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(ChatListUiState.Error("The server is unavailable right now."), viewModel.uiState.value)
    }

    @Test
    fun `retry re-fetches from Loading`() = runTest {
        var calls = 0
        val repository = FakeWhatsAppRepository(
            onChats = {
                calls += 1
                if (calls == 1) ChatsResult.Error("failed") else ChatsResult.Empty
            },
        )
        val viewModel = ChatListViewModel(repository)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(ChatListUiState.Error("failed"), viewModel.uiState.value)

        viewModel.retry()

        assertEquals(ChatListUiState.Loading, viewModel.uiState.value)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(ChatListUiState.Empty, viewModel.uiState.value)
        assertEquals(2, calls)
    }
}
