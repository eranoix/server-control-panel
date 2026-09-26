// The `ViewModelConstructorInComposable` check exists for a PRODUCTION
// hazard: a ViewModel constructed inside a composable is reborn on every
// recomposition, losing state and leaking scope. In a Compose test that
// renders ONCE with a fake ViewModel injected, that does not happen — and
// injecting the fake into `setContent` is precisely the seam the test exists
// to exercise. The suppression belongs to the TEST FILE, and to it alone;
// production code stays subject to the rule.
@file:Suppress("ViewModelConstructorInComposable")

package com.vpsmanager.feature.jira

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.jira.JiraResult
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

/**
 * Renders the real board under Robolectric.
 *
 * There is no emulator in this environment, and a board screen without a
 * render test is exactly the kind of piece that compiles and arrives crooked
 * on the device — that is how a whole screen once shipped here with no caller.
 */
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

    /**
     * The owner's requirement, in the form of a test: all THREE columns on
     * screen at once, with cards from more than one visible together.
     *
     * The previous version showed one column per page — which is what Trello
     * and the official Jira do on mobile, and which turns a board into a list
     * with tabs. This test fails if anyone goes back to the pager.
     */
    @Test
    fun `as tres colunas aparecem AO MESMO TEMPO, com os cartoes de cada uma`() {
        val source = FakeSource(
            JiraResult.Ok(
                testBoard(
                    toDo = listOf(card("TASK-1"), card("TASK-2")),
                    inProgress = listOf(card("TASK-3", "Em Progresso", "indeterminate")),
                    done = listOf(card("TASK-4", "Pronto", "done")),
                ),
            ),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithTag(TAG_BOARD).assertIsDisplayed()
        compose.onNodeWithText("A fazer").assertIsDisplayed()
        compose.onNodeWithText("Em andamento").assertIsDisplayed()
        compose.onNodeWithText("Concluído").assertIsDisplayed()

        // Cards from THREE different columns, all drawn at once — this is
        // what a pager cannot do.
        compose.onNodeWithText("TASK-1").assertIsDisplayed()
        compose.onNodeWithText("TASK-3").assertIsDisplayed()
        compose.onNodeWithText("TASK-4").assertIsDisplayed()
    }

    @Test
    fun `cada coluna mostra quantos cartoes tem`() {
        val source = FakeSource(
            JiraResult.Ok(
                testBoard(toDo = listOf(card("TASK-1"), card("TASK-2"))),
            ),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("2").assertIsDisplayed()
    }

    @Test
    fun `a primeira coluna aparece com os cartoes dela`() {
        val source = FakeSource(
            JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("TASK-1").assertIsDisplayed()
        compose.onNodeWithText("resumo de TASK-1").assertIsDisplayed()
    }

    @Test
    fun `coluna vazia diz o que esta vazio, e nao so um espaco em branco`() {
        val source = FakeSource(JiraResult.Ok(testBoard()))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        // A 125 dp column has no room for a sentence; it has room for the
        // word that answers the question ("is there anything here?"). The
        // detail lives in the sheet, one tap away.
        compose.onAllNodesWithText("empty").assertCountEquals(3)
    }

    @Test
    fun `sem conta ligada, a tela e o formulario de conexao`() {
        val source = FakeSource(JiraResult.Ok(testBoard().copy(connected = false)))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithTag(TAG_CONNECTION).assertIsDisplayed()
        compose.onNodeWithText("Connect to Jira").assertIsDisplayed()
    }

    @Test
    fun `a recusa do Jira aparece SEM apagar os controles`() {
        // A malformed JQL must not cost the project selector and the filters
        // — they are what lets you undo the narrowing that caused the refusal.
        val source = FakeSource(
            JiraResult.Ok(testBoard(rejection = "JQL inválido perto de 'ORDER'")),
        )
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("JQL inválido perto de 'ORDER'").assertIsDisplayed()
        compose.onNodeWithText("VPSM").assertIsDisplayed()
        compose.onNodeWithText("Todas").assertIsDisplayed()
    }

    @Test
    fun `os filtros desenhados sao os que o SERVIDOR mandou`() {
        // A list hard-coded in the client would go stale on its own the day
        // the panel gained a new filter — that is how the two surfaces
        // diverged last time.
        val source = FakeSource(JiraResult.Ok(testBoard()))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("Todas").assertIsDisplayed()
        compose.onNodeWithText("Minhas").assertIsDisplayed()
    }

    @Test
    fun `tocar num filtro recarrega o quadro com ele`() {
        val source = FakeSource(JiraResult.Ok(testBoard()))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }
        val before = source.boardsRequested

        compose.onNodeWithText("Minhas").performClick()
        compose.waitForIdle()

        assert(source.boardsRequested > before) { "tocar no filtro tinha que pedir o quadro de novo" }
    }

    @Test
    fun `tocar num cartao abre a folha da issue`() {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("resumo de TASK-1").performClick()
        compose.waitForIdle()

        compose.onNodeWithTag(TAG_ISSUE_SHEET).assertIsDisplayed()
    }

    @Test
    fun `um erro de carga oferece tentar de novo`() {
        val source = FakeSource(JiraResult.Error("Falha de conexão."))
        compose.setContent { JiraBoardRoute(vm = BoardViewModel(source)) }

        compose.onNodeWithText("Falha de conexão.").assertIsDisplayed()
        compose.onNodeWithText("Try again").assertIsDisplayed()
    }
}
