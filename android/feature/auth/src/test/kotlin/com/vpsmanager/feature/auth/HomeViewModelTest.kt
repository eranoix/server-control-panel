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
 * Home state transitions: first load, a failed reload that keeps the current
 * data, and auto-refresh that stays idle when it should.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class HomeViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)

    @After fun tearDown() = Dispatchers.resetMain()

    @Test
    fun `first load goes from Loading to the dashboard`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })

        assertEquals(HomeUiState.Loading, vm.uiState.value)
        advanceUntilIdle()

        val state = vm.uiState.value as HomeUiState.Success
        assertEquals("tester", state.snapshot.identity?.user)
        assertEquals(null, state.staleError)
    }

    @Test
    fun `a failed reload keeps the dashboard and flags it stale`() = runTest(dispatcher) {
        var calls = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                calls += 1
                if (calls == 1) {
                    DashboardResult.Success(snapshotReal())
                } else {
                    DashboardResult.Error("The server is unavailable right now.")
                }
            },
        )
        advanceUntilIdle()

        vm.refresh()
        advanceUntilIdle()

        val state = vm.uiState.value as HomeUiState.Success
        assertEquals("The server is unavailable right now.", state.staleError)
        assertEquals(false, state.refreshing)
        assertTrue(state.snapshot.resourceSignals.isNotEmpty())
    }

    @Test
    fun `a failed first load becomes a hard error, with nothing to keep`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Error("Connection failed.") })
        advanceUntilIdle()

        assertEquals(HomeUiState.Error("Connection failed."), vm.uiState.value)
    }

    @Test
    fun `retry goes back to the skeleton and then to the dashboard`() = runTest(dispatcher) {
        var calls = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                calls += 1
                if (calls == 1) DashboardResult.Error("down") else DashboardResult.Success(snapshotReal())
            },
        )
        advanceUntilIdle()

        vm.load()
        assertEquals(HomeUiState.Loading, vm.uiState.value)
        advanceUntilIdle()

        assertTrue(vm.uiState.value is HomeUiState.Success)
    }

    @Test
    fun `auto-refresh does not run over an error screen`() = runTest(dispatcher) {
        var calls = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                calls += 1
                DashboardResult.Error("down")
            },
        )
        advanceUntilIdle()
        val afterFirst = calls

        vm.autoRefresh()
        advanceUntilIdle()

        // Polling over an error would only make it flicker; the user decides to retry.
        assertEquals(afterFirst, calls)
    }

    @Test
    fun `auto-refresh is silent and does not show the refresh indicator`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        advanceUntilIdle()

        vm.autoRefresh()
        assertEquals(false, (vm.uiState.value as HomeUiState.Success).refreshing)
    }

    @Test
    fun `pull to refresh shows the indicator until the response arrives`() = runTest(dispatcher) {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        advanceUntilIdle()

        vm.refresh()
        assertEquals(true, (vm.uiState.value as HomeUiState.Success).refreshing)

        advanceUntilIdle()
        assertEquals(false, (vm.uiState.value as HomeUiState.Success).refreshing)
    }

    @Test
    fun `a successful reload clears the stale warning`() = runTest(dispatcher) {
        var calls = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                calls += 1
                if (calls == 2) DashboardResult.Error("down") else DashboardResult.Success(snapshotReal())
            },
        )
        advanceUntilIdle()
        vm.refresh()
        advanceUntilIdle()
        assertEquals("down", (vm.uiState.value as HomeUiState.Success).staleError)

        vm.refresh()
        advanceUntilIdle()
        assertEquals(null, (vm.uiState.value as HomeUiState.Success).staleError)
    }
}
