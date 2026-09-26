package com.vpsmanager.feature.jira

import com.vpsmanager.data.jira.FalhaEmLote
import com.vpsmanager.data.jira.ResultadoDoJira
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
class QuadroViewModelTest {

    private val despachante = StandardTestDispatcher()

    @Before
    fun ligarMain() = Dispatchers.setMain(despachante)

    @After
    fun soltarMain() = Dispatchers.resetMain()

    private fun colunas(vm: QuadroViewModel) =
        (vm.estado.value as EstadoDoQuadro.Pronto).quadro.colunas

    private fun chavesEm(vm: QuadroViewModel, rotulo: String) =
        colunas(vm).first { it.rotulo == rotulo }.cartoes.map { it.chave }

    @Test
    fun `sem conta ligada a tela e o formulario de conexao, nao um erro`() = runTest(despachante) {
        val fonte = FonteFalsa(
            ResultadoDoJira.Ok(quadroDeTeste().copy(conectado = false)),
        )
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        assertEquals(EstadoDoQuadro.Desconectado, vm.estado.value)
    }

    @Test
    fun `o cartao muda de coluna ANTES da resposta do servidor`() = runTest(despachante) {
        // Without this, the card would sit pinned under the finger for a
        // whole network round trip — hundreds of milliseconds in which the
        // screen contradicts the gesture.
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-1", "Em andamento")
        // Without advancing the dispatcher: the state has to have changed already.
        assertEquals(listOf("TASK-1"), chavesEm(vm, "Em andamento"))
        assertTrue(chavesEm(vm, "A fazer").isEmpty())
    }

    @Test
    fun `recusa do fluxo de trabalho devolve o cartao para a coluna e a POSICAO de origem`() =
        runTest(despachante) {
            // The position matters: putting it back at the end of a long
            // column would make the card "disappear" even though it returned.
            val fonte = FonteFalsa(
                quadro = ResultadoDoJira.Ok(
                    quadroDeTeste(aFazer = listOf(cartao("TASK-1"), cartao("TASK-2"), cartao("TASK-3"))),
                ),
                aoMover = { _, _ -> ResultadoDoJira.Recusa("o fluxo não leva TASK-2 para \"Concluído\"") },
            )
            val vm = QuadroViewModel(fonte)
            advanceUntilIdle()

            vm.mover("TASK-2", "Concluído")
            advanceUntilIdle()

            assertEquals(listOf("TASK-1", "TASK-2", "TASK-3"), chavesEm(vm, "A fazer"))
            assertTrue(chavesEm(vm, "Concluído").isEmpty())
        }

    @Test
    fun `a recusa vira recado com o motivo do SERVIDOR, nao uma frase generica`() = runTest(despachante) {
        // The server's message is the only one that says where you CAN go
        // from there — and that is what turns the refusal into a next step.
        val motivo = "o fluxo de trabalho não leva TASK-1 para \"Concluído\"; daqui só dá para ir a: Em Progresso"
        val fonte = FonteFalsa(
            quadro = ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))),
            aoMover = { _, _ -> ResultadoDoJira.Recusa(motivo) },
        )
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-1", "Concluído")
        advanceUntilIdle()

        assertEquals(motivo, vm.recado.value)
    }

    @Test
    fun `falha de rede tambem devolve o cartao`() = runTest(despachante) {
        val fonte = FonteFalsa(
            quadro = ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))),
            aoMover = { _, _ -> ResultadoDoJira.Erro("Falha de conexão.") },
        )
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-1", "Em andamento")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1"), chavesEm(vm, "A fazer"))
    }

    @Test
    fun `soltar na coluna onde o cartao ja esta nao chama o servidor`() = runTest(despachante) {
        // It happens all the time: pick the card up, change your mind, drop it
        // where it was. A real transition there would be a change nobody asked
        // for.
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-1", "A fazer")
        advanceUntilIdle()

        assertTrue("nao podia ter chamado o servidor: ${fonte.movimentos}", fonte.movimentos.isEmpty())
    }

    @Test
    fun `mover manda o ROTULO da coluna, nunca um id de transicao`() = runTest(despachante) {
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-1", "Em andamento")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1" to "Em andamento"), fonte.movimentos)
    }

    @Test
    fun `mover um cartao que nao esta no quadro nao faz nada`() = runTest(despachante) {
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-404", "Concluído")
        advanceUntilIdle()

        assertTrue(fonte.movimentos.isEmpty())
        assertEquals(listOf("TASK-1"), chavesEm(vm, "A fazer"))
    }

    @Test
    fun `trocar de projeto FIXA a escolha no servidor`() = runTest(despachante) {
        // It is the same preference the web panel uses: changing it here
        // changes it there, and the choice survives the app's next launch.
        val fonte = FonteFalsa()
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.trocarProjeto("TTW")
        advanceUntilIdle()

        assertEquals(listOf("TTW"), fonte.projetosFixados)
    }

    @Test
    fun `a selecao e podada quando o filtro tira as issues do quadro`() = runTest(despachante) {
        // Without pruning, the counter would say "2 ticked" with none on screen.
        val fonte = FonteFalsa(
            ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1"), cartao("TASK-2")))),
        )
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.alternarSelecao("TASK-1")
        vm.alternarSelecao("TASK-2")
        assertEquals(setOf("TASK-1", "TASK-2"), vm.selecao.value)

        fonte.devolverQuadro(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        vm.trocarFiltro("mine")
        advanceUntilIdle()

        assertEquals(setOf("TASK-1"), vm.selecao.value)
    }

    @Test
    fun `sair do modo de selecao limpa o que estava marcado`() = runTest(despachante) {
        val vm = QuadroViewModel(FonteFalsa())
        advanceUntilIdle()

        vm.alternarModoSelecao()
        vm.alternarSelecao("TASK-1")
        vm.alternarModoSelecao()

        assertTrue(vm.selecao.value.isEmpty())
    }

    @Test
    fun `atribuir a mim usa o accountId que o SERVIDOR disse ser meu`() = runTest(despachante) {
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.atribuirAMim("TASK-1")
        advanceUntilIdle()

        assertEquals(listOf("TASK-1" to "acc-eu"), fonte.atribuicoes)
    }

    @Test
    fun `sem saber quem eu sou, atribuir a mim avisa em vez de mandar vazio`() = runTest(despachante) {
        // Sending an empty accountId would UNASSIGN — the exact opposite of
        // what was asked.
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")), eu = null)))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.atribuirAMim("TASK-1")
        advanceUntilIdle()

        assertTrue(fonte.atribuicoes.isEmpty())
        assertEquals("The server did not say who you are in Jira.", vm.recado.value)
    }

    @Test
    fun `mover em lote sem nada marcado nao chama o servidor`() = runTest(despachante) {
        val fonte = FonteFalsa()
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.moverSelecao("Concluído")
        advanceUntilIdle()

        assertTrue(fonte.lotesMovidos.isEmpty())
    }

    @Test
    fun `recarregar nao volta para Carregando quando ja ha quadro na tela`() = runTest(despachante) {
        // Replacing the board with a spinner on every filter makes the screen
        // flash white and loses the scroll position.
        val vm = QuadroViewModel(FonteFalsa())
        advanceUntilIdle()
        assertTrue(vm.estado.value is EstadoDoQuadro.Pronto)

        vm.trocarFiltro("mine")
        assertTrue(
            "durante a recarga o quadro tem que continuar na tela",
            vm.estado.value is EstadoDoQuadro.Pronto,
        )
    }

    @Test
    fun `o recado e consumido uma vez so`() = runTest(despachante) {
        val fonte = FonteFalsa(ResultadoDoJira.Ok(quadroDeTeste(aFazer = listOf(cartao("TASK-1")))))
        val vm = QuadroViewModel(fonte)
        advanceUntilIdle()

        vm.mover("TASK-1", "Em andamento")
        advanceUntilIdle()
        assertEquals("TASK-1 → Em andamento", vm.recado.value)

        vm.consumirRecado()
        assertNull(vm.recado.value)
    }
}

class ResumoDoLoteTest {

    @Test
    fun `lote inteiro certo diz so quantas`() {
        assertEquals("3 moved", resumoDoLote(3, emptyList()))
    }

    @Test
    fun `uma falha sozinha traz o motivo inteiro`() {
        assertEquals(
            "TASK-2: sem transição",
            resumoDoLote(0, listOf(FalhaEmLote("TASK-2", "sem transição"))),
        )
    }

    @Test
    fun `falhas sao NOMEADAS — sao elas que exigem acao`() {
        // A bare "2 failed" would force you to compare the board before with
        // the board after to work out which.
        val frase = resumoDoLote(
            1,
            listOf(FalhaEmLote("TASK-2", "sem transição"), FalhaEmLote("TASK-3", "sem transição")),
        )
        assertTrue(frase, frase.contains("TASK-2") && frase.contains("TASK-3"))
    }

    @Test
    fun `acima de tres, a frase ainda cabe numa tarja`() {
        val falhas = (1..6).map { FalhaEmLote("VPSM-$it", "sem transição") }
        val frase = resumoDoLote(0, falhas)
        assertTrue(frase, frase.contains("and 3 more"))
        assertTrue("a frase ficou longa demais: $frase", frase.length < 120)
    }
}
