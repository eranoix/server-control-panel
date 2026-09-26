package com.vpsmanager.feature.auth

import com.vpsmanager.data.dashboard.DashboardResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * The Home screen's state transitions: first load, a reload that preserves the
 * current picture, and the automatic loop that does nothing when it should not.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class HomeViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    @Test
    fun `primeira carga sai de Loading para o painel`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })

        assertEquals(HomeUiState.Loading, vm.uiState.value)
        advanceUntilIdle()

        val estado = vm.uiState.value as HomeUiState.Success
        assertEquals("teste", estado.snapshot.identity?.user)
        assertEquals(null, estado.staleError)
    }

    @Test
    fun `recarga que falha PRESERVA o painel e carimba o aviso`() = runTest(dispatcher) {
        var chamadas = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                chamadas += 1
                if (chamadas == 1) {
                    DashboardResult.Success(snapshotReal())
                } else {
                    DashboardResult.Error("O servidor está indisponível no momento.")
                }
            },
        )
        advanceUntilIdle()

        vm.refresh()
        advanceUntilIdle()

        val estado = vm.uiState.value as HomeUiState.Success
        assertEquals("O servidor está indisponível no momento.", estado.staleError)
        assertEquals(false, estado.refreshing)
        // the previous picture stays intact
        assertTrue(estado.snapshot.resourceSignals.isNotEmpty())
    }

    @Test
    fun `falha na PRIMEIRA carga vira erro duro — nao ha quadro a preservar`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Error("Falha de conexão.") })
        advanceUntilIdle()

        assertEquals(HomeUiState.Error("Falha de conexão."), vm.uiState.value)
    }

    @Test
    fun `tentar novamente volta ao esqueleto e depois ao painel`() = runTest(dispatcher) {
        var chamadas = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                chamadas += 1
                if (chamadas == 1) DashboardResult.Error("caiu") else DashboardResult.Success(snapshotReal())
            },
        )
        advanceUntilIdle()

        vm.load()
        assertEquals(HomeUiState.Loading, vm.uiState.value)
        advanceUntilIdle()

        assertTrue(vm.uiState.value is HomeUiState.Success)
    }

    @Test
    fun `atualizacao automatica nao roda por cima de uma tela de erro`() = runTest(dispatcher) {
        var chamadas = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                chamadas += 1
                DashboardResult.Error("caiu")
            },
        )
        advanceUntilIdle()
        val depoisDaPrimeira = chamadas

        vm.autoRefresh()
        advanceUntilIdle()

        // A loop every 5 s on top of an error message would only make the
        // message flicker; the operator is the one who decides to leave that
        // state.
        assertEquals(depoisDaPrimeira, chamadas)
    }

    @Test
    fun `atualizacao automatica e silenciosa — nao acende o indicador de recarga`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        advanceUntilIdle()

        vm.autoRefresh()
        assertEquals(false, (vm.uiState.value as HomeUiState.Success).refreshing)
    }

    @Test
    fun `puxar para atualizar acende o indicador ate a resposta chegar`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        advanceUntilIdle()

        vm.refresh()
        assertEquals(true, (vm.uiState.value as HomeUiState.Success).refreshing)

        advanceUntilIdle()
        assertEquals(false, (vm.uiState.value as HomeUiState.Success).refreshing)
    }

    @Test
    fun `uma recarga bem-sucedida limpa o aviso de quadro velho`() = runTest(dispatcher) {
        var chamadas = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                chamadas += 1
                if (chamadas == 2) DashboardResult.Error("caiu") else DashboardResult.Success(snapshotReal())
            },
        )
        advanceUntilIdle()
        vm.refresh()
        advanceUntilIdle()
        assertEquals("caiu", (vm.uiState.value as HomeUiState.Success).staleError)

        vm.refresh()
        advanceUntilIdle()
        assertEquals(null, (vm.uiState.value as HomeUiState.Success).staleError)
    }
}
