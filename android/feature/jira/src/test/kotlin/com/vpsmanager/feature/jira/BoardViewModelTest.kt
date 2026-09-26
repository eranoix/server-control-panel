package com.vpsmanager.feature.jira

import com.vpsmanager.data.jira.BulkFailure
import com.vpsmanager.data.jira.JiraResult
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
    fun `sem conta ligada a tela e o formulario de conexao, nao um erro`() = runTest(dispatcher) {
        val source = FakeSource(
            JiraResult.Ok(testBoard().copy(connected = false)),
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        assertEquals(BoardState.Disconnected, vm.state.value)
    }

    @Test
    fun `o cartao muda de coluna ANTES da resposta do servidor`() = runTest(dispatcher) {
        // Without this, the card would sit pinned under the finger for a
        // whole network round trip — hundreds of milliseconds in which the
        // screen contradicts the gesture.
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "Em andamento")
        // Without advancing the dispatcher: the state has to have changed already.
        assertEquals(listOf("TASK-1"), keysIn(vm, "Em andamento"))
        assertTrue(keysIn(vm, "A fazer").isEmpty())
    }

    @Test
    fun `recusa do fluxo de trabalho devolve o cartao para a coluna e a POSICAO de origem`() =
        runTest(dispatcher) {
            // The position matters: putting it back at the end of a long
            // column would make the card "disappear" even though it returned.
            val source = FakeSource(
                board = JiraResult.Ok(
                    testBoard(toDo = listOf(card("TASK-1"), card("TASK-2"), card("TASK-3"))),
                ),
                onMove = { _, _ -> JiraResult.Rejected("o fluxo não leva TASK-2 para \"Concluído\"") },
            )
            val vm = BoardViewModel(source)
            advanceUntilIdle()

            vm.move("TASK-2", "Concluído")
            advanceUntilIdle()

            assertEquals(listOf("TASK-1", "TASK-2", "TASK-3"), keysIn(vm, "A fazer"))
            assertTrue(keysIn(vm, "Concluído").isEmpty())
        }

    @Test
    fun `a recusa vira recado com o motivo do SERVIDOR, nao uma frase generica`() = runTest(dispatcher) {
        // The server's message is the only one that says where you CAN go
        // from there — and that is what turns the refusal into a next step.
        val reason = "o fluxo de trabalho não leva TASK-1 para \"Concluído\"; daqui só dá para ir a: Em Progresso"
        val source = FakeSource(
            board = JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))),
            onMove = { _, _ -> JiraResult.Rejected(reason) },
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "Concluído")
        advanceUntilIdle()

        assertEquals(reason, vm.notice.value)
    }

    @Test
    fun `falha de rede tambem devolve o cartao`() = runTest(dispatcher) {
        val source = FakeSource(
            board = JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))),
            onMove = { _, _ -> JiraResult.Error("Falha de conexão.") },
        )
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "Em andamento")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1"), keysIn(vm, "A fazer"))
    }

    @Test
    fun `soltar na coluna onde o cartao ja esta nao chama o servidor`() = runTest(dispatcher) {
        // It happens all the time: pick the card up, change your mind, drop it
        // where it was. A real transition there would be a change nobody asked
        // for.
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "A fazer")
        advanceUntilIdle()

        assertTrue("nao podia ter chamado o servidor: ${source.moves}", source.moves.isEmpty())
    }

    @Test
    fun `mover manda o ROTULO da coluna, nunca um id de transicao`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "Em andamento")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1" to "Em andamento"), source.moves)
    }

    @Test
    fun `mover um cartao que nao esta no quadro nao faz nada`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-404", "Concluído")
        advanceUntilIdle()

        assertTrue(source.moves.isEmpty())
        assertEquals(listOf("TASK-1"), keysIn(vm, "A fazer"))
    }

    @Test
    fun `trocar de projeto FIXA a escolha no servidor`() = runTest(dispatcher) {
        // It is the same preference the web panel uses: changing it here
        // changes it there, and the choice survives the app's next launch.
        val source = FakeSource()
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.switchProject("TTW")
        advanceUntilIdle()

        assertEquals(listOf("TTW"), source.pinnedProjects)
    }

    @Test
    fun `a selecao e podada quando o filtro tira as issues do quadro`() = runTest(dispatcher) {
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
    fun `sair do modo de selecao limpa o que estava marcado`() = runTest(dispatcher) {
        val vm = BoardViewModel(FakeSource())
        advanceUntilIdle()

        vm.toggleSelectionMode()
        vm.toggleSelection("TASK-1")
        vm.toggleSelectionMode()

        assertTrue(vm.selection.value.isEmpty())
    }

    @Test
    fun `atribuir a mim usa o accountId que o SERVIDOR disse ser meu`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.assignToMe("TASK-1")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1" to "acc-eu"), source.assignments)
    }

    @Test
    fun `sem saber quem eu sou, atribuir a mim avisa em vez de mandar vazio`() = runTest(dispatcher) {
        // Sending an empty accountId would UNASSIGN — the exact opposite of
        // what was asked.
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")), me = null)))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.assignToMe("TASK-1")
        advanceUntilIdle()

        assertTrue(source.assignments.isEmpty())
        assertEquals("The server did not say who you are in Jira.", vm.notice.value)
    }

    @Test
    fun `mover em lote sem nada marcado nao chama o servidor`() = runTest(dispatcher) {
        val source = FakeSource()
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.moveSelection("Concluído")
        advanceUntilIdle()

        assertTrue(source.bulkMoves.isEmpty())
    }

    @Test
    fun `recarregar nao volta para Carregando quando ja ha quadro na tela`() = runTest(dispatcher) {
        // Replacing the board with a spinner on every filter makes the screen
        // flash white and loses the scroll position.
        val vm = BoardViewModel(FakeSource())
        advanceUntilIdle()
        assertTrue(vm.state.value is BoardState.Ready)

        vm.switchFilter("mine")
        assertTrue(
            "durante a recarga o quadro tem que continuar na tela",
            vm.state.value is BoardState.Ready,
        )
    }

    @Test
    fun `o recado e consumido uma vez so`() = runTest(dispatcher) {
        val source = FakeSource(JiraResult.Ok(testBoard(toDo = listOf(card("TASK-1")))))
        val vm = BoardViewModel(source)
        advanceUntilIdle()

        vm.move("TASK-1", "Em andamento")
        advanceUntilIdle()
        assertEquals("TASK-1 → Em andamento", vm.notice.value)

        vm.consumeNotice()
        assertNull(vm.notice.value)
    }
}

class BulkSummaryTest {

    @Test
    fun `lote inteiro certo diz so quantas`() {
        assertEquals("3 moved", bulkSummary(3, emptyList()))
    }

    @Test
    fun `uma falha sozinha traz o motivo inteiro`() {
        assertEquals(
            "TASK-2: sem transição",
            bulkSummary(0, listOf(BulkFailure("TASK-2", "sem transição"))),
        )
    }

    @Test
    fun `falhas sao NOMEADAS — sao elas que exigem acao`() {
        // A bare "2 failed" would force you to compare the board before with
        // the board after to work out which.
        val sentence = bulkSummary(
            1,
            listOf(BulkFailure("TASK-2", "sem transição"), BulkFailure("TASK-3", "sem transição")),
        )
        assertTrue(sentence, sentence.contains("TASK-2") && sentence.contains("TASK-3"))
    }

    @Test
    fun `acima de tres, a frase ainda cabe numa tarja`() {
        val failures = (1..6).map { BulkFailure("VPSM-$it", "sem transição") }
        val sentence = bulkSummary(0, failures)
        assertTrue(sentence, sentence.contains("and 3 more"))
        assertTrue("a frase ficou longa demais: $sentence", sentence.length < 120)
    }
}
