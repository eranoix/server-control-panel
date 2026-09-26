package com.vpsmanager.feature.videocall

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.videocall.VideocallRoom
import com.vpsmanager.data.videocall.VideocallRoomsResult
import com.vpsmanager.data.videocall.VideocallRoomsSource
import kotlinx.coroutines.awaitCancellation
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [RoomLobbyScreen] under Robolectric across every [RoomLobbyUiState] -- never composed
 * before this.
 */
@RunWith(RobolectricTestRunner::class)
class RoomLobbyScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private class FakeRoomsSource(private val onRooms: suspend () -> VideocallRoomsResult) : VideocallRoomsSource {
        override suspend fun rooms(): VideocallRoomsResult = onRooms()
    }

    @Test
    fun `loading state shows a spinner, not a blank screen`() {
        val source = FakeRoomsSource { awaitCancellation() }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = RoomLobbyViewModel(source)
        composeRule.setContent { RoomLobbyScreen(onRoomSelected = {}, viewModel = viewModel) }

        composeRule.onNodeWithText("Loading rooms…").assertExists()
    }

    @Test
    fun `success lists every room and clicking one navigates into it`() {
        val source = FakeRoomsSource {
            VideocallRoomsResult.Success(
                listOf(VideocallRoom(id = "sala-1", name = "Reunião de equipe", memberCount = 3)),
            )
        }
        var selectedRoomId: String? = null
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm = RoomLobbyViewModel(source)
        composeRule.setContent {
            RoomLobbyScreen(onRoomSelected = { selectedRoomId = it }, viewModel = vm)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Reunião de equipe").assertExists()
        composeRule.onNodeWithText("3 participant(s)").assertExists()
        composeRule.onNodeWithText("Reunião de equipe").performClick()
        assert(selectedRoomId == "sala-1")
    }

    @Test
    fun `empty room list renders the create-a-room message instead of a blank list`() {
        val source = FakeRoomsSource { VideocallRoomsResult.Empty }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = RoomLobbyViewModel(source)
        composeRule.setContent { RoomLobbyScreen(onRoomSelected = {}, viewModel = viewModel) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("No rooms yet").assertExists()
    }

    @Test
    fun `error state surfaces the reason and retry reloads the room list`() {
        var calls = 0
        val source = FakeRoomsSource {
            calls += 1
            if (calls == 1) VideocallRoomsResult.Error("Não foi possível falar com o servidor de sinalização.")
            else VideocallRoomsResult.Success(listOf(VideocallRoom(id = "sala-1", name = "Reunião", memberCount = 1)))
        }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val viewModel = RoomLobbyViewModel(source)
        composeRule.setContent { RoomLobbyScreen(onRoomSelected = {}, viewModel = viewModel) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Não foi possível falar com o servidor de sinalização.").assertExists()
        composeRule.onNodeWithText("Try again").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Reunião").assertExists()
    }
}
