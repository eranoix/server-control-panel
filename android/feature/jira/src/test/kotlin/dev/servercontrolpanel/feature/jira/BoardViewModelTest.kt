package dev.servercontrolpanel.feature.jira

import dev.servercontrolpanel.data.jira.BulkFailure
import dev.servercontrolpanel.data.jira.JiraResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class BoardViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUpMain() = Dispatchers.setMain(dispatcher)

    @After
    fun resetMain() = Dispatchers.resetMain()

    private fun columns(vm: BoardViewModel) =
        (vm.state.value as BoardState.Ready).board.columns

    private fun keysIn(vm: BoardViewModel, label: String) =
        columns(vm).first { it.label == label }.cards.map { it.key }

    @Test
    fun `without a linked account the screen is the connection form, not an error`() = runTest(dispatcher) {
        val source = FakeSource(
            JiraResult.Ok(testBoard().copy(connected = false)),
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        assertEquals(BoardState.Disconnected, vm.state.value)
    }

    @Test
    fun `the card changes column BEFORE the server responds`() = runTest(dispatcher) {
        // Otherwise the screen contradicts the gesture for a whole network round trip.
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "In Progress")
        // No dispatcher advance: the state must already have changed.
        assertEquals(listOf("TASK-1"), keysIn(vm, "In Progress"))
        assertTrue(keysIn(vm, "To Do").isEmpty())
    }

    @Test
    fun `a workflow refusal puts the card back in its original column and POSITION`() =
        runTest(dispatcher) {
            // Putting it back at the end of a long column would make the card seem to disappear.
            val source = FakeSource(
                board = JiraResult.Ok(
                    testBoard(toDo = listOf(card("TASK-1"), card("TASK-2"), card("TASK-3"))),
                ),
                onMove = { _, _ -> JiraResult.Rejected("the workflow does not take TASK-2 to \"Done\"") },
            )
            val vm = BoardViewModel(source)
            advanceUntilIdle()

            vm.move("TASK-2", "Done")
            advanceUntilIdle()

            assertEquals(listOf("TASK-1", "TASK-2", "TASK-3"), keysIn(vm, "To Do"))
            assertTrue(keysIn(vm, "Done").isEmpty())
        }

    @Test
    fun `a refusal becomes a notice with the SERVER's reason, not a generic sentence`() = runTest(dispatcher) {
        // Only the server's message says where the card CAN go next.
        val reason = "the workflow does not take TASK-1 to \"Done\"; from here it can only go to: In Progress"
        val source = FakeSource(
            board = JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))),
            onMove = { _, _ -> JiraResult.Rejected(reason) },
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "Done")
        advanceUntilIdle()

        assertEquals(reason, vm.notice.value)
    }

    @Test
    fun `a network failure also puts the card back`() = runTest(dispatcher) {
        val source = FakeSource(
            board = JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))),
            onMove = { _, _ -> JiraResult.Error("Connection failed.") },
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "In Progress")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1"), keysIn(vm, "To Do"))
    }

    @Test
    fun `dropping on the card's current column does not call the server`() = runTest(dispatcher) {
        // Picking a card up and dropping it back is common; a transition there would be unasked for.
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "To Do")
        advanceUntilIdle()

        assertTrue("should not have called the server: ${source.moves}", source.moves.isEmpty())
    }

    @Test
    fun `moving sends the column LABEL, never a transition id`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "In Progress")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1" to "In Progress"), source.moves)
    }

    @Test
    fun `moving a card that is not on the board does nothing`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-404", "Done")
        advanceUntilIdle()

        assertTrue(source.moves.isEmpty())
        assertEquals(listOf("TASK-1"), keysIn(vm, "To Do"))
    }

    @Test
    fun `switching project PINS the choice on the server`() = runTest(dispatcher) {
        // Same preference the web panel uses, so it syncs and survives an app restart.
        val source = FakeSource()
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.switchProject("TTW")
        advanceUntilIdle()

        assertEquals(listOf("TTW"), source.pinnedProjects)
    }

    @Test
    fun `the selection is pruned when the filter removes issues from the board`() = runTest(dispatcher) {
        // Without pruning, the counter would say "2 ticked" with none on screen.
        val source = FakeSource(
            JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1"), card("TASK-2")))),
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.toggleSelection("TASK-1")
        vm.toggleSelection("TASK-2")
        assertEquals(setOf("TASK-1", "TASK-2"), vm.selection.value)

        source.returnBoard(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        vm.switchFilter("mine")
        advanceUntilIdle()

        assertEquals(setOf("TASK-1"), vm.selection.value)
    }

    @Test
    fun `leaving selection mode clears what was ticked`() = runTest(dispatcher) {
        val vm = BoardViewModel(FakeSource())
        advanceUntilIdle()

        vm.toggleSelectionMode()
        vm.toggleSelection("TASK-1")
        vm.toggleSelectionMode()

        assertTrue(vm.selection.value.isEmpty())
    }

    @Test
    fun `assign to me uses the accountId the SERVER said is mine`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.assignToMe("TASK-1")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1" to "acc-eu"), source.assignments)
    }

    @Test
    fun `without knowing who I am, assign to me warns instead of sending empty`() = runTest(dispatcher) {
        // An empty accountId would UNASSIGN, the opposite of what was asked.
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")), me = null)))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.assignToMe("TASK-1")
        advanceUntilIdle()

        assertTrue(source.assignments.isEmpty())
        assertEquals("The server did not say who you are in Jira.", vm.notice.value)
    }

    @Test
    fun `bulk move with nothing ticked does not call the server`() = runTest(dispatcher) {
        val source = FakeSource()
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.moveSelection("Done")
        advanceUntilIdle()

        assertTrue(source.bulkMoves.isEmpty())
    }

    @Test
    fun `reloading does not go back to Loading when a board is already on screen`() = runTest(dispatcher) {
        // A spinner on every filter change flashes the screen and loses the scroll position.
        val vm = BoardViewModel(FakeSource())
        advanceUntilIdle()
        assertTrue(vm.state.value is BoardState.Ready)

        vm.switchFilter("mine")
        assertTrue(
            "the board must stay on screen while reloading",
            vm.state.value is BoardState.Ready,
        )
    }

    @Test
    fun `the notice is consumed only once`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "In Progress")
        advanceUntilIdle()
        assertEquals("TASK-1 → In Progress", vm.notice.value)

        vm.consumeNotice()
        assertNull(vm.notice.value)
    }
}

class BulkSummaryTest {

    @Test
    fun `an all-successful batch only says how many`() {
        assertEquals("3 moved", bulkSummary(3, emptyList()))
    }

    @Test
    fun `a single failure carries the full reason`() {
        assertEquals(
            "TASK-2: no transition",
            bulkSummary(0, listOf(BulkFailure("TASK-2", "no transition"))),
        )
    }

    @Test
    fun `failures are NAMED because they need action`() {
        // A bare "2 failed" would force a before/after board comparison to find which.
        val sentence = bulkSummary(
            1,
            listOf(BulkFailure("TASK-2", "no transition"), BulkFailure("TASK-3", "no transition")),
        )
        assertTrue(sentence, sentence.contains("TASK-2") && sentence.contains("TASK-3"))
    }

    @Test
    fun `above three, the sentence still fits in a banner`() {
        val failures = (1..6).map { BulkFailure("PANEL-$it", "no transition") }
        val sentence = bulkSummary(0, failures)
        assertTrue(sentence, sentence.contains("and 3 more"))
        assertTrue("the sentence got too long: $sentence", sentence.length < 120)
    }
}
