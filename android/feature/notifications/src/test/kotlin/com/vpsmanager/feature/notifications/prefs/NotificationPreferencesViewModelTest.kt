package com.vpsmanager.feature.notifications.prefs

import com.vpsmanager.data.push.NotifyPreferencesResult
import com.vpsmanager.data.push.NotifyPreferencesSource
import com.vpsmanager.data.push.NotifyRule
import com.vpsmanager.data.push.UpdateNotifyPreferencesResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private class FakeNotifyPreferencesSource(
    private var fetchResult: NotifyPreferencesResult,
    private val updateResult: UpdateNotifyPreferencesResult = UpdateNotifyPreferencesResult.Success,
) : NotifyPreferencesSource {
    var lastUpdateDeviceId: String? = null
        private set
    var lastUpdateEnabledRuleIds: List<String>? = null
        private set
    var updateCallCount = 0
        private set

    override suspend fun fetch(deviceId: String): NotifyPreferencesResult = fetchResult

    override suspend fun update(deviceId: String, enabledRuleIds: List<String>): UpdateNotifyPreferencesResult {
        updateCallCount++
        lastUpdateDeviceId = deviceId
        lastUpdateEnabledRuleIds = enabledRuleIds
        return updateResult
    }
}

@OptIn(ExperimentalCoroutinesApi::class)
class NotificationPreferencesViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    private val deployFailed = NotifyRule(
        id = "deploy-failed",
        name = "Deploy falhou",
        minSeverity = "critical",
        typePrefix = "job.",
        enabledForDevice = true,
    )
    private val deployDone = NotifyRule(
        id = "deploy-done",
        name = "Deploy concluído",
        minSeverity = "info",
        typePrefix = "job.",
        enabledForDevice = false,
    )

    @Test
    fun `starts Loading then renders the rules from the server response`() = runTest {
        val source = FakeNotifyPreferencesSource(NotifyPreferencesResult.Success(listOf(deployFailed, deployDone)))
        val viewModel = NotificationPreferencesViewModel(deviceId = "device-123", repository = source)

        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value
        check(state is NotificationPreferencesUiState.Success)
        assertEquals(listOf(deployFailed, deployDone), state.rules)
    }

    @Test
    fun `a fetch failure surfaces LoadError`() = runTest {
        val source = FakeNotifyPreferencesSource(NotifyPreferencesResult.Error("falhou"))
        val viewModel = NotificationPreferencesViewModel(deviceId = "device-123", repository = source)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(NotificationPreferencesUiState.LoadError("falhou"), viewModel.uiState.value)
    }

    @Test
    fun `toggling a rule issues exactly one PUT with the updated enabled set for this device`() = runTest {
        val source = FakeNotifyPreferencesSource(NotifyPreferencesResult.Success(listOf(deployFailed, deployDone)))
        val viewModel = NotificationPreferencesViewModel(deviceId = "device-123", repository = source)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.setRuleEnabled(ruleId = "deploy-done", enabled = true)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, source.updateCallCount)
        assertEquals("device-123", source.lastUpdateDeviceId)
        assertEquals(setOf("deploy-failed", "deploy-done"), source.lastUpdateEnabledRuleIds?.toSet())
        val state = viewModel.uiState.value
        check(state is NotificationPreferencesUiState.Success)
        assertTrue(state.rules.first { it.id == "deploy-done" }.enabledForDevice)
    }

    @Test
    fun `a failed PUT reverts the toggle and surfaces an error`() = runTest {
        val source = FakeNotifyPreferencesSource(
            fetchResult = NotifyPreferencesResult.Success(listOf(deployFailed, deployDone)),
            updateResult = UpdateNotifyPreferencesResult.Error("falha de rede"),
        )
        val viewModel = NotificationPreferencesViewModel(deviceId = "device-123", repository = source)
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.setRuleEnabled(ruleId = "deploy-done", enabled = true)
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value
        check(state is NotificationPreferencesUiState.Success)
        assertEquals("falha de rede", state.errorMessage)
        assertTrue(!state.rules.first { it.id == "deploy-done" }.enabledForDevice)
    }
}
