package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import dev.servercontrolpanel.data.terminal.TerminalSession
import dev.servercontrolpanel.data.terminal.TerminalSessionsResult
import dev.servercontrolpanel.data.terminal.TerminalSessionsSource
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertThrows
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [SessionListScreen] under Robolectric in every [SessionListUiState].
 */
@RunWith(RobolectricTestRunner::class)
class SessionListScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows the progress indicator`() {
        val source = FakeSessionsSource { awaitCancellation() }
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm = SessionListViewModel(source)
        composeRule.setContent {
            SessionListScreen(onSessionSelected = {}, viewModel = vm)
        }

        composeRule.onNodeWithText("Loading sessions…").assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        val source = FakeSessionsSource { TerminalSessionsResult.Error("The server is unavailable right now.") }
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm2 = SessionListViewModel(source)
        composeRule.setContent {
            SessionListScreen(onSessionSelected = {}, viewModel = vm2)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The server is unavailable right now.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `empty state offers a new-session field instead of an empty list`() {
        val source = FakeSessionsSource { TerminalSessionsResult.Empty }
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm3 = SessionListViewModel(source)
        composeRule.setContent {
            SessionListScreen(onSessionSelected = {}, viewModel = vm3)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("No open sessions").assertExists()
        composeRule.onNodeWithText("Session name").assertExists()
    }

    @Test
    fun `attaching from the empty state's field navigates with the typed name`() {
        val source = FakeSessionsSource { TerminalSessionsResult.Empty }
        var selected: String? = null
        // Built outside setContent so recomposition does not create a new ViewModel.
        val vm4 = SessionListViewModel(source)
        composeRule.setContent {
            SessionListScreen(onSessionSelected = { selected = it }, viewModel = vm4)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Session name").performTextInput("build")
        composeRule.onNodeWithText("Attach").performClick()

        assert(selected == "build") { "expected onSessionSelected(\"build\"), got $selected" }
    }

    /**
     * [SessionListContent] keys its `LazyColumn` by session name, which the server
     * contract guarantees unique. Duplicates would be a server bug, so they are not
     * masked client-side; this test documents the resulting crash.
     */
    @Test
    fun `two sessions sharing the same name crash the list instead of degrading gracefully`() {
        val duplicateNamed = listOf(
            TerminalSession(name = "build", attached = true, created = 1L, tab = null),
            TerminalSession(name = "build", attached = false, created = 2L, tab = null),
        )
        val source = FakeSessionsSource { TerminalSessionsResult.Success(duplicateNamed) }

        assertThrows(IllegalArgumentException::class.java) {
            // Built outside setContent so recomposition does not create a new ViewModel.
            val vm5 = SessionListViewModel(source)
            composeRule.setContent {
                SessionListScreen(onSessionSelected = {}, viewModel = vm5)
            }
            composeRule.waitForIdle()
        }
    }
}

private class FakeSessionsSource(
    private val onSessions: suspend () -> TerminalSessionsResult,
) : TerminalSessionsSource {
    override suspend fun sessions(): TerminalSessionsResult = onSessions()
}
