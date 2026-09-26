package com.vpsmanager.feature.admin.ops

import com.vpsmanager.data.events.EventsSubscriber
import com.vpsmanager.data.events.MobileEvent
import com.vpsmanager.data.ops.DeployStatus
import com.vpsmanager.data.ops.DeployStatusResult
import com.vpsmanager.data.ops.OpsSource
import com.vpsmanager.data.ops.OpsStatusResult
import com.vpsmanager.data.ops.TriggerDeployResult
import com.vpsmanager.data.session.SessionResult
import com.vpsmanager.data.session.SessionSource
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

private class FakeOpsSource(
    var triggerResult: TriggerDeployResult = TriggerDeployResult.Success(jobId = "job-1"),
    var deployStatusResult: DeployStatusResult = DeployStatusResult.Success(DeployStatus(status = "done", progress = 100, step = null, error = null)),
) : OpsSource {
    var triggerCallCount = 0

    override suspend fun fetchStatus(): OpsStatusResult = OpsStatusResult.Error("não usado neste teste")

    override suspend fun triggerDeploy(): TriggerDeployResult {
        triggerCallCount += 1
        return triggerResult
    }

    override suspend fun fetchDeployStatus(jobId: String): DeployStatusResult = deployStatusResult
}

private class FakeEventsSubscriber : EventsSubscriber {
    val flowsByChannel = mutableMapOf<String, MutableSharedFlow<MobileEvent>>()

    override fun subscribe(channel: String): Flow<MobileEvent> =
        flowsByChannel.getOrPut(channel) { MutableSharedFlow(extraBufferCapacity = 8) }
}

private class FakeSessionSource(private val result: SessionResult) : SessionSource {
    override suspend fun getMe(): SessionResult = result
}

private fun deployEvent(channel: String, json: String) =
    MobileEvent(v = 1, channel = channel, type = "deploy", data = Json.parseToJsonElement(json))

@OptIn(ExperimentalCoroutinesApi::class)
class DeployTriggerViewModelTest {

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
    fun `starts Idle and never calls triggerDeploy before confirmation`() = runTest {
        val repository = FakeOpsSource()
        val viewModel = DeployTriggerViewModel(eventsClient = FakeEventsSubscriber(), repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        assertEquals(DeployTriggerUiState.Idle, viewModel.uiState.value)
        assertEquals(0, repository.triggerCallCount)
    }

    @Test
    fun `requestConfirmation opens the dialog without triggering a deploy`() = runTest {
        val repository = FakeOpsSource()
        val viewModel = DeployTriggerViewModel(eventsClient = FakeEventsSubscriber(), repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()

        assertEquals(DeployTriggerUiState.AwaitingConfirmation, viewModel.uiState.value)
        assertEquals(0, repository.triggerCallCount)
    }

    @Test
    fun `dismissConfirmation returns to Idle without triggering a deploy`() = runTest {
        val repository = FakeOpsSource()
        val viewModel = DeployTriggerViewModel(eventsClient = FakeEventsSubscriber(), repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()
        viewModel.dismissConfirmation()

        assertEquals(DeployTriggerUiState.Idle, viewModel.uiState.value)
        assertEquals(0, repository.triggerCallCount)
    }

    @Test
    fun `confirmDeploy is a no-op unless the dialog is open`() = runTest {
        val repository = FakeOpsSource()
        val viewModel = DeployTriggerViewModel(eventsClient = FakeEventsSubscriber(), repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.confirmDeploy()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(DeployTriggerUiState.Idle, viewModel.uiState.value)
        assertEquals(0, repository.triggerCallCount)
    }

    @Test
    fun `confirming triggers exactly one deploy call and enters InProgress queued`() = runTest {
        val repository = FakeOpsSource()
        val events = FakeEventsSubscriber()
        val viewModel = DeployTriggerViewModel(eventsClient = events, repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()
        viewModel.confirmDeploy()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, repository.triggerCallCount)
        val state = viewModel.uiState.value
        check(state is DeployTriggerUiState.InProgress)
        assertEquals("job-1", state.jobId)
        assertEquals("queued", state.phase)
        assertTrue(events.flowsByChannel.containsKey("deploy.job-1"))
    }

    @Test
    fun `a trigger failure surfaces TriggerFailed and never subscribes to a channel`() = runTest {
        val repository = FakeOpsSource(triggerResult = TriggerDeployResult.Error("fila indisponível"))
        val events = FakeEventsSubscriber()
        val viewModel = DeployTriggerViewModel(eventsClient = events, repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()
        viewModel.confirmDeploy()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(DeployTriggerUiState.TriggerFailed("fila indisponível"), viewModel.uiState.value)
        assertTrue(events.flowsByChannel.isEmpty())
    }

    @Test
    fun `log and progress events stream into the InProgress state`() = runTest {
        val repository = FakeOpsSource()
        val events = FakeEventsSubscriber()
        val viewModel = DeployTriggerViewModel(eventsClient = events, repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()
        viewModel.confirmDeploy()
        dispatcher.scheduler.advanceUntilIdle()

        val channel = events.flowsByChannel.getValue("deploy.job-1")
        channel.emit(deployEvent("deploy.job-1", """{"type":"status","job_id":"job-1","status":"running","ts":1}"""))
        channel.emit(deployEvent("deploy.job-1", """{"type":"log","job_id":"job-1","log":"buildando...","ts":2}"""))
        channel.emit(deployEvent("deploy.job-1", """{"type":"progress","job_id":"job-1","progress":42,"ts":3}"""))
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value
        check(state is DeployTriggerUiState.InProgress)
        assertEquals("running", state.phase)
        assertEquals(listOf("buildando..."), state.logLines)
        assertEquals(42, state.progress)
    }

    @Test
    fun `a terminal status event fetches the real outcome and renders it verbatim`() = runTest {
        val repository = FakeOpsSource(
            deployStatusResult = DeployStatusResult.Success(
                DeployStatus(status = "failed", progress = 60, step = "health-check", error = "gate de saude falhou; rollback automatico executado"),
            ),
        )
        val events = FakeEventsSubscriber()
        val viewModel = DeployTriggerViewModel(eventsClient = events, repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()
        viewModel.confirmDeploy()
        dispatcher.scheduler.advanceUntilIdle()

        val channel = events.flowsByChannel.getValue("deploy.job-1")
        channel.emit(deployEvent("deploy.job-1", """{"type":"status","job_id":"job-1","status":"failed","ts":9}"""))
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            DeployTriggerUiState.Outcome(status = "failed", error = "gate de saude falhou; rollback automatico executado"),
            viewModel.uiState.value,
        )
    }

    @Test
    fun `requestConfirmation is ignored while a deploy is InProgress`() = runTest {
        val repository = FakeOpsSource()
        val events = FakeEventsSubscriber()
        val viewModel = DeployTriggerViewModel(eventsClient = events, repository = repository, sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)))

        viewModel.requestConfirmation()
        viewModel.confirmDeploy()
        dispatcher.scheduler.advanceUntilIdle()

        viewModel.requestConfirmation()

        assertTrue(viewModel.uiState.value is DeployTriggerUiState.InProgress)
        assertEquals(1, repository.triggerCallCount)
    }

    @Test
    fun `isAdmin resolves to true for an admin session`() = runTest {
        val viewModel = DeployTriggerViewModel(
            eventsClient = FakeEventsSubscriber(),
            repository = FakeOpsSource(),
            sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)),
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(true, viewModel.isAdmin.value)
    }

    @Test
    fun `isAdmin resolves to false for a non-admin session`() = runTest {
        val viewModel = DeployTriggerViewModel(
            eventsClient = FakeEventsSubscriber(),
            repository = FakeOpsSource(),
            sessionRepository = FakeSessionSource(SessionResult.Success(user = "convidado", email = "guest@northwind.example", isAdmin = false)),
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(false, viewModel.isAdmin.value)
    }

    @Test
    fun `isAdmin resolves to false when the session is empty`() = runTest {
        val viewModel = DeployTriggerViewModel(
            eventsClient = FakeEventsSubscriber(),
            repository = FakeOpsSource(),
            sessionRepository = FakeSessionSource(SessionResult.Empty),
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(false, viewModel.isAdmin.value)
    }

    @Test
    fun `isAdmin resolves to false when the session fails to load`() = runTest {
        val viewModel = DeployTriggerViewModel(
            eventsClient = FakeEventsSubscriber(),
            repository = FakeOpsSource(),
            sessionRepository = FakeSessionSource(SessionResult.Error("falha de rede")),
        )

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(false, viewModel.isAdmin.value)
    }

    @Test
    fun `isAdmin is null until the session check resolves`() = runTest {
        val viewModel = DeployTriggerViewModel(
            eventsClient = FakeEventsSubscriber(),
            repository = FakeOpsSource(),
            sessionRepository = FakeSessionSource(SessionResult.Success(user = "sam", email = "sam@northwind.example", isAdmin = true)),
        )

        assertEquals(null, viewModel.isAdmin.value)
    }
}
