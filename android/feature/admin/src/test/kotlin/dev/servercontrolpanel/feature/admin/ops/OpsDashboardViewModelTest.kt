package dev.servercontrolpanel.feature.admin.ops

import dev.servercontrolpanel.data.events.EventsSubscriber
import dev.servercontrolpanel.data.events.MobileEvent
import dev.servercontrolpanel.data.ops.OpsAlert
import dev.servercontrolpanel.data.ops.OpsSnapshot
import dev.servercontrolpanel.data.ops.OpsSource
import dev.servercontrolpanel.data.ops.OpsStatusResult
import dev.servercontrolpanel.data.ops.DeployStatusResult
import dev.servercontrolpanel.data.ops.TriggerDeployResult
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

private val sampleSnapshot = OpsSnapshot(
    health = mapOf("docker" to "ok"),
    healthOk = true,
    queueRunning = 1,
    queueQueued = 0,
    alerts = listOf(OpsAlert(name = "cpu-high", severity = "critical", state = "firing", currentValue = 95.0, threshold = 90.0, unit = "%")),
)

private class DashboardFakeOpsSource(
    private var statusResult: OpsStatusResult = OpsStatusResult.Success(sampleSnapshot),
) : OpsSource {
    override suspend fun fetchStatus(): OpsStatusResult = statusResult
    override suspend fun triggerDeploy(): TriggerDeployResult = TriggerDeployResult.Error("not used in this test")
    override suspend fun fetchDeployStatus(jobId: String): DeployStatusResult = DeployStatusResult.Error("not used in this test")
}

private class DashboardFakeEventsSubscriber : EventsSubscriber {
    val opsHealth = MutableSharedFlow<MobileEvent>(extraBufferCapacity = 8)
    var lastSubscribedChannel: String? = null

    override fun subscribe(channel: String): Flow<MobileEvent> {
        lastSubscribedChannel = channel
        return opsHealth
    }
}

@OptIn(ExperimentalCoroutinesApi::class)
class OpsDashboardViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `starts Loading then renders the initial status fetch`() = runTest {
        val events = DashboardFakeEventsSubscriber()
        val viewModel = OpsDashboardViewModel(eventsClient = events, repository = DashboardFakeOpsSource())

        assertEquals(OpsDashboardUiState.Loading, viewModel.uiState.value)

        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value
        check(state is OpsDashboardUiState.Success)
        assertEquals(sampleSnapshot, state.snapshot)
    }

    @Test
    fun `a fetch failure surfaces LoadError`() = runTest {
        val events = DashboardFakeEventsSubscriber()
        val viewModel = OpsDashboardViewModel(
            eventsClient = events,
            repository = DashboardFakeOpsSource(statusResult = OpsStatusResult.Error("network failure")),
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(OpsDashboardUiState.LoadError("network failure"), viewModel.uiState.value)
    }

    @Test
    fun `subscribes exactly to the ops health channel`() = runTest {
        val events = DashboardFakeEventsSubscriber()
        OpsDashboardViewModel(eventsClient = events, repository = DashboardFakeOpsSource())

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("ops.health", events.lastSubscribedChannel)
    }

    @Test
    fun `a live ops health event replaces the rendered snapshot without a refetch`() = runTest {
        val events = DashboardFakeEventsSubscriber()
        val viewModel = OpsDashboardViewModel(eventsClient = events, repository = DashboardFakeOpsSource())
        dispatcher.scheduler.advanceUntilIdle()

        val updatedJson = Json.parseToJsonElement(
            """
            {
              "health": {"docker": "ok"},
              "health_ok": false,
              "queue_running": 3,
              "queue_queued": 5,
              "alerts": []
            }
            """.trimIndent(),
        )
        events.opsHealth.emit(MobileEvent(v = 1, channel = "ops.health", type = "ops.status", data = updatedJson))
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value
        check(state is OpsDashboardUiState.Success)
        assertEquals(false, state.snapshot.healthOk)
        assertEquals(3L, state.snapshot.queueRunning)
        assertEquals(5L, state.snapshot.queueQueued)
        assertTrue(state.snapshot.alerts.isEmpty())
    }

    @Test
    fun `a malformed live event is ignored and the last known snapshot is kept`() = runTest {
        val events = DashboardFakeEventsSubscriber()
        val viewModel = OpsDashboardViewModel(eventsClient = events, repository = DashboardFakeOpsSource())
        dispatcher.scheduler.advanceUntilIdle()

        events.opsHealth.emit(
            MobileEvent(v = 1, channel = "ops.health", type = "ops.status", data = Json.parseToJsonElement("""{"not":"recognized"}""")),
        )
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value
        check(state is OpsDashboardUiState.Success)
        assertEquals(sampleSnapshot, state.snapshot)
    }
}
