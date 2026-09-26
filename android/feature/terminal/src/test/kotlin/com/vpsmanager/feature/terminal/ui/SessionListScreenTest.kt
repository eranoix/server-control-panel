package com.vpsmanager.feature.terminal.ui

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.vpsmanager.data.terminal.TerminalSession
import com.vpsmanager.data.terminal.TerminalSessionsResult
import com.vpsmanager.data.terminal.TerminalSessionsSource
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertThrows
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Renders [SessionListScreen] under Robolectric in every [SessionListUiState]
 * -- never composed before this.
 */
@RunWith(RobolectricTestRunner::class)
class SessionListScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading state shows the progress indicator`() {
        val source = FakeSessionsSource { awaitCancellation() }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm = SessionListViewModel(source)
        composeRule.setContent {
            SessionListScreen(onSessionSelected = {}, viewModel = vm)
        }

        composeRule.onNodeWithText("Loading sessions…").assertExists()
    }

    @Test
    fun `error state surfaces the reason and offers retry`() {
        val source = FakeSessionsSource { TerminalSessionsResult.Error("O servidor está indisponível no momento.") }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
        val vm2 = SessionListViewModel(source)
        composeRule.setContent {
            SessionListScreen(onSessionSelected = {}, viewModel = vm2)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("O servidor está indisponível no momento.").assertExists()
        composeRule.onNodeWithText("Try again").assertExists()
    }

    @Test
    fun `empty state offers a new-session field instead of an empty list`() {
        val source = FakeSessionsSource { TerminalSessionsResult.Empty }
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
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
        // Construido FORA do setContent: a lambda de conteudo recompoe, e
        // construir la dentro daria um ViewModel novo a cada recomposicao.
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
     * FINDING (not fixed here): [SessionListContent]'s
     * `LazyColumn(key = { it.name })` has the exact same unguarded-key shape
     * that crashed `ShareDestinationScreen` on duplicate `displayName`s. This
     * screen only survives today because `TerminalSession.name` is expected
     * to be unique per the server's dtach session contract -- unlike shared
     * file names, a duplicate here would mean the BFF itself returned bad
     * data, not a routine client-side collision. Deduplicating client-side
     * (as `disambiguateSharedItems` does for shares) would silently mask
     * that server-side contract violation instead of surfacing it, so this
     * is reported rather than auto-fixed. This test documents the current,
     * real crash so it isn't a silent landmine.
     */
    @Test
    fun `two sessions sharing the same name crash the list instead of degrading gracefully`() {
        val duplicateNamed = listOf(
            TerminalSession(name = "build", attached = true, created = 1L, tab = null),
            TerminalSession(name = "build", attached = false, created = 2L, tab = null),
        )
        val source = FakeSessionsSource { TerminalSessionsResult.Success(duplicateNamed) }

        assertThrows(IllegalArgumentException::class.java) {
            // Built OUTSIDE setContent: the content lambda recomposes, and
            // building it in there would give a new ViewModel on every
            // recomposition.
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
