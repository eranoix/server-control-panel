// ViewModelConstructorInComposable guards a production hazard (recreation on
// recomposition). These tests render once with an injected fake, so the rule is
// suppressed for this test file only.
@file:Suppress("ViewModelConstructorInComposable")

package dev.servercontrolpanel.feature.jira

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.data.jira.JiraResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Renders the real board under Robolectric. */
@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class BoardScreenTest {

    @get:Rule
    val compose = createComposeRule()

    @Before
    fun setUpMain() = Dispatchers.setMain(UnconfinedTestDispatcher())

    @After
    fun resetMain() = Dispatchers.resetMain()

    /** Requirement: all THREE columns on screen at once; fails if the board becomes a pager. */
    @Test
    fun `the three columns appear AT THE SAME TIME, each with its cards`() {
        val source = FakeSource(
            JiraResult.Ok(
                testBoard(
                    toDo = listOf(card("TASK-1"), card("TASK-2")),
                    inProgress = listOf(card("TASK-3", "In Progress", "indeterminate")),
                    done = listOf(card("TASK-4", "Done", "done")),
                ),
            ),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithTag(TAG_BOARD).assertIsDisplayed()
        compose.onNodeWithText("To Do").assertIsDisplayed()
        compose.onNodeWithText("In Progress").assertIsDisplayed()
        compose.onNodeWithText("Done").assertIsDisplayed()

        // Cards from three different columns drawn at once, which a pager cannot do.
        compose.onNodeWithText("TASK-1").assertIsDisplayed()
        compose.onNodeWithText("TASK-3").assertIsDisplayed()
        compose.onNodeWithText("TASK-4").assertIsDisplayed()
    }

    @Test
    fun `each column shows how many cards it has`() {
        val source = FakeSource(
            JiraResult.Ok(
                testBoard(toDo = listOf(card("TASK-1"), card("TASK-2"))),
            ),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("2").assertIsDisplayed()
    }

    @Test
    fun `the first column appears with its cards`() {
        val source = FakeSource(
            JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("TASK-1").assertIsDisplayed()
        compose.onNodeWithText("summary of TASK-1").assertIsDisplayed()
    }

    @Test
    fun `an empty column says it is empty, not just blank space`() {
        val source = FakeSource(JiraResult.Ok(testBoard()))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        // A 125 dp column only has room for one word, not a sentence.
        compose.onAllNodesWithText("empty").assertCountEquals(3)
    }

    @Test
    fun `without a linked account, the screen is the connection form`() {
        val source = FakeSource(JiraResult.Ok(testBoard().copy(connected = false)))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithTag(TAG_CONNECTION).assertIsDisplayed()
        compose.onNodeWithText("Connect to Jira").assertIsDisplayed()
    }

    @Test
    fun `a Jira refusal shows WITHOUT hiding the controls`() {
        // The project selector and filters are what let you undo the query that caused the refusal.
        val source = FakeSource(
            JiraResult.Ok(testBoard(rejection = "invalid JQL near 'ORDER'")),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("invalid JQL near 'ORDER'").assertIsDisplayed()
        compose.onNodeWithText("PANEL").assertIsDisplayed()
        compose.onNodeWithText("All").assertIsDisplayed()
    }

    @Test
    fun `the filters drawn are the ones the SERVER sent`() {
        // A hard-coded client list would go stale when the panel gains a filter.
        val source = FakeSource(JiraResult.Ok(testBoard()))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("All").assertIsDisplayed()
        compose.onNodeWithText("Mine").assertIsDisplayed()
    }

    @Test
    fun `tapping a filter reloads the board with it`() {
        val source = FakeSource(JiraResult.Ok(testBoard()))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }
        val before = source.boardsRequested

        compose.onNodeWithText("Mine").performClick()
        compose.waitForIdle()

        assert(source.boardsRequested > before) { "tapping the filter should have requested the board again" }
    }

    @Test
    fun `tapping a card opens the issue sheet`() {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("summary of TASK-1").performClick()
        compose.waitForIdle()

        compose.onNodeWithTag(TAG_ISSUE_SHEET).assertIsDisplayed()
    }

    @Test
    fun `a load error offers to try again`() {
        val source = FakeSource(JiraResult.Error("Connection failed."))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("Connection failed.").assertIsDisplayed()
        compose.onNodeWithText("Try again").assertIsDisplayed()
    }
}
