package dev.servercontrolpanel.feature.terminal.ui

import dev.servercontrolpanel.data.terminal.ActionResult
import dev.servercontrolpanel.data.terminal.TargetsResult
import dev.servercontrolpanel.data.terminal.BackupsResult
import dev.servercontrolpanel.data.terminal.PreviewResult
import dev.servercontrolpanel.data.terminal.TerminalBackupSource
import dev.servercontrolpanel.data.terminal.TerminalSession
import dev.servercontrolpanel.data.terminal.TerminalSessionsResult
import dev.servercontrolpanel.data.terminal.TerminalSessionsSource
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test

private class FakeTerminalSessionsSource(
    private val results: MutableList<TerminalSessionsResult>,
) : TerminalSessionsSource {
    var callCount = 0
        private set

    override suspend fun sessions(): TerminalSessionsResult {
        callCount++
        return if (results.size > 1) results.removeAt(0) else results.first()
    }
}

private class FakeBackupSource(
    private val targets: TargetsResult = TargetsResult.Success(listOf("sam", "jordan", "*")),
    private var preview: PreviewResult = PreviewResult.Success("$ ls\nbin  etc", 2),
) : TerminalBackupSource {
    val killed = mutableListOf<String>()
    val assigned = mutableListOf<Pair<String, String>>()
    var previewsRequested = 0
        private set

    override suspend fun backups() = BackupsResult.Empty
    override suspend fun createBackup(session: String?) = ActionResult.Ok("ok")
    override suspend fun restore(id: String, session: String?) = ActionResult.Ok("ok")
    override suspend fun deleteBackup(id: String, session: String?) = ActionResult.Ok("ok")
    override suspend fun renameSession(from: String, to: String) = ActionResult.Ok("ok")

    override suspend fun killSession(name: String): ActionResult {
        killed += name
        return ActionResult.Ok("Session $name ended.")
    }

    override suspend fun assignSession(name: String, target: String): ActionResult {
        assigned += name to target
        return ActionResult.Ok("$name is now visible to $target.")
    }

    override suspend fun sessionPreview(name: String, lines: Int): PreviewResult {
        previewsRequested++
        return preview
    }

    override suspend fun assignmentTargets(): TargetsResult = targets

    fun setPreview(r: PreviewResult) { preview = r }
}

@OptIn(ExperimentalCoroutinesApi::class)
class SessionListViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        dispatcher.scheduler.advanceUntilIdle()
        Dispatchers.resetMain()
    }

    @Test
    fun `starts Loading then moves to Success with the fetched sessions`() = runTest {
        val session = TerminalSession(name = "main", attached = true, created = 1000, tab = "shell")
        val source = FakeTerminalSessionsSource(mutableListOf(TerminalSessionsResult.Success(listOf(session))))
        val viewModel = SessionListViewModel(sessionsSource = source)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(SessionListUiState.Success(listOf(session)), viewModel.uiState.value)
    }

    @Test
    fun `maps an empty session list to Empty`() = runTest {
        val source = FakeTerminalSessionsSource(mutableListOf(TerminalSessionsResult.Empty))
        val viewModel = SessionListViewModel(sessionsSource = source)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(SessionListUiState.Empty, viewModel.uiState.value)
    }

    @Test
    fun `maps a repository failure to Error with its message`() = runTest {
        val source = FakeTerminalSessionsSource(mutableListOf(TerminalSessionsResult.Error("failed")))
        val viewModel = SessionListViewModel(sessionsSource = source)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(SessionListUiState.Error("failed"), viewModel.uiState.value)
    }

    @Test
    fun `refresh re-queries the source and can recover from an earlier error`() = runTest {
        val session = TerminalSession(name = "main", attached = true, created = 1000, tab = "shell")
        val source = FakeTerminalSessionsSource(
            mutableListOf(
                TerminalSessionsResult.Error("failed"),
                TerminalSessionsResult.Success(listOf(session)),
            ),
        )
        val viewModel = SessionListViewModel(sessionsSource = source)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(SessionListUiState.Error("failed"), viewModel.uiState.value)

        viewModel.refresh()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(SessionListUiState.Success(listOf(session)), viewModel.uiState.value)
        assertEquals(2, source.callCount)
    }

    private fun vmWith(
        backup: FakeBackupSource,
        sessions: List<TerminalSession> = listOf(
            TerminalSession(name = "main", attached = true, created = 1000, tab = null),
        ),
    ): SessionListViewModel = SessionListViewModel(
        sessionsSource = FakeTerminalSessionsSource(
            mutableListOf(TerminalSessionsResult.Success(sessions)),
        ),
        backupSource = backup,
    )

    @Test
    fun `kill calls the server once and reloads the list`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.kill("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(listOf("main"), backup.killed)
        assertEquals("Session main ended.", vm.notice.value)
    }

    @Test
    fun `assign sends the chosen target unchanged`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.assign("main", "*")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(listOf("main" to "*"), backup.assigned)
    }

    @Test
    fun `peek opens and peeking again closes without asking the server twice`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(PreviewUiState.Ready("$ ls\nbin  etc"), vm.previews.value["main"])
        assertEquals(1, backup.previewsRequested)

        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(null, vm.previews.value["main"])
        assertEquals("closing does not request anything from the server", 1, backup.previewsRequested)
    }

    @Test
    fun `closing the preview while loading does not reopen it`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.togglePreview("main")
        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(null, vm.previews.value["main"])
    }

    @Test
    fun `an empty preview is its own state, not an error`() = runTest {
        val backup = FakeBackupSource(preview = PreviewResult.Success("   ", 0))
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PreviewUiState.Empty, vm.previews.value["main"])
    }

    @Test
    fun `without permission the targets are empty and the screen hides the option`() = runTest {
        val backup = FakeBackupSource(targets = TargetsResult.Error("not found"))
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(null, vm.targets.value)

        vm.loadTargets()
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(emptyList<String>(), vm.targets.value)
    }

    @Test
    fun `targets are requested once, not every time the menu opens`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmWith(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.loadTargets()
        dispatcher.scheduler.advanceUntilIdle()
        val afterFirst = vm.targets.value
        assertEquals(listOf("sam", "jordan", "*"), afterFirst)

        vm.loadTargets()
        vm.loadTargets()
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(afterFirst, vm.targets.value)
    }

}
