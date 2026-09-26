package dev.servercontrolpanel.feature.videocall

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.data.videocall.VideocallRoom
import dev.servercontrolpanel.data.videocall.VideocallRoomsResult
import dev.servercontrolpanel.data.videocall.VideocallRoomsSource
import kotlinx.coroutines.awaitCancellation
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Renders [RoomLobbyScreen] under Robolectric across every [RoomLobbyUiState]. */
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
        // Built outside setContent so recomposition does not create a new ViewModel.
        val viewModel = RoomLobbyViewModel(source)
        composeRule.setContent { RoomLobbyScreen(onRoomSelected = {}, viewModel = viewModel) }

        composeRule.onNodeWithText("Loading rooms…").assertExists()
    }

    @Test
    fun `success lists every room and clicking one navigates into it`() {
        val source = FakeRoomsSource {
            VideocallRoomsResult.Success(
                listOf(VideocallRoom(id = "sala-1", name = "Team meeting", memberCount = 3)),
            )
        }
        var selectedRoomId: String? = null
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm = RoomLobbyViewModel(source)
        composeRule.setContent {
            RoomLobbyScreen(onRoomSelected = { selectedRoomId = it }, viewModel = vm)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Team meeting").assertExists()
        composeRule.onNodeWithText("3 participant(s)").assertExists()
        composeRule.onNodeWithText("Team meeting").performClick()
        assert(selectedRoomId == "sala-1")
    }

    @Test
    fun `empty room list renders the create-a-room message instead of a blank list`() {
        val source = FakeRoomsSource { VideocallRoomsResult.Empty }
        // Built outside setContent so recomposition does not create a new ViewModel.
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
            if (calls == 1) VideocallRoomsResult.Error("Could not reach the signalling server.")
            else VideocallRoomsResult.Success(listOf(VideocallRoom(id = "sala-1", name = "Meeting", memberCount = 1)))
        }
        // Built outside setContent so recomposition does not create a new ViewModel.
        val viewModel = RoomLobbyViewModel(source)
        composeRule.setContent { RoomLobbyScreen(onRoomSelected = {}, viewModel = viewModel) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Could not reach the signalling server.").assertExists()
        composeRule.onNodeWithText("Try again").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Meeting").assertExists()
    }
}
