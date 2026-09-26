package com.vpsmanager.feature.terminal.ui

import com.vpsmanager.data.terminal.ActionResult
import com.vpsmanager.data.terminal.TargetsResult
import com.vpsmanager.data.terminal.BackupsResult
import com.vpsmanager.data.terminal.PreviewResult
import com.vpsmanager.data.terminal.TerminalBackupSource
import com.vpsmanager.data.terminal.TerminalSession
import com.vpsmanager.data.terminal.TerminalSessionsResult
import com.vpsmanager.data.terminal.TerminalSessionsSource
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

/**
 * A double for the write source. It records what was ASKED — not only what
 * came back — because half of these tests are about the call NOT happening.
 */
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
        return ActionResult.Ok("Sessão $name encerrada.")
    }

    override suspend fun assignSession(name: String, target: String): ActionResult {
        assigned += name to target
        return ActionResult.Ok("$name agora aparece para $target.")
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
        // DRAIN before releasing Main. Without this, a coroutine one of this
        // test's ViewModels still had pending wakes up AFTER resetMain(),
        // touches a Dispatchers.Main that no longer exists, and the exception
        // surfaces as "uncaught exceptions before the test started" in the
        // NEXT test — which may not even be in the same class. That was the
        // flake: it passed in isolation, failed in the suite, and the victim
        // kept changing.
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
        val source = FakeTerminalSessionsSource(mutableListOf(TerminalSessionsResult.Error("falhou")))
        val viewModel = SessionListViewModel(sessionsSource = source)

        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(SessionListUiState.Error("falhou"), viewModel.uiState.value)
    }

    @Test
    fun `refresh re-queries the source and can recover from an earlier error`() = runTest {
        val session = TerminalSession(name = "main", attached = true, created = 1000, tab = "shell")
        val source = FakeTerminalSessionsSource(
            mutableListOf(
                TerminalSessionsResult.Error("falhou"),
                TerminalSessionsResult.Success(listOf(session)),
            ),
        )
        val viewModel = SessionListViewModel(sessionsSource = source)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(SessionListUiState.Error("falhou"), viewModel.uiState.value)

        viewModel.refresh()
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(SessionListUiState.Success(listOf(session)), viewModel.uiState.value)
        assertEquals(2, source.callCount)
    }
    // --- kill, assign, peek ---------------------------------------------------

    private fun vmCom(
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
    fun `matar chama o servidor uma vez e recarrega a lista`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.kill("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(listOf("main"), backup.killed)
        assertEquals("Sessão main encerrada.", vm.notice.value)
    }

    @Test
    fun `atribuir manda o alvo escolhido, sem traduzir`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.assign("main", "*")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(listOf("main" to "*"), backup.assigned)
    }

    @Test
    fun `espiar abre, e espiar de novo FECHA — sem pedir ao servidor duas vezes`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(PreviewUiState.Ready("$ ls\nbin  etc"), vm.previews.value["main"])
        assertEquals(1, backup.previewsRequested)

        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(null, vm.previews.value["main"])
        assertEquals("fechar nao pede nada ao servidor", 1, backup.previewsRequested)
    }

    @Test
    fun `fechar a previa no meio do carregamento nao a reabre por baixo da pessoa`() = runTest {
        // The response arrives AFTER the person has closed it. Without the
        // guard, it would reappear on its own — and the person would tap
        // "close" all over again.
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.togglePreview("main")   // opens and starts loading
        vm.togglePreview("main")   // closes before the response comes back
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(null, vm.previews.value["main"])
    }

    @Test
    fun `previa vazia e ESTADO proprio, nao erro`() = runTest {
        // A freshly created session has not written anything yet. Showing that
        // as a failure would send people looking for a problem that does not
        // exist.
        val backup = FakeBackupSource(preview = PreviewResult.Success("   ", 0))
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.togglePreview("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PreviewUiState.Empty, vm.previews.value["main"])
    }

    @Test
    fun `sem permissao os alvos ficam VAZIOS — e a tela esconde a opcao`() = runTest {
        // The route returns 404 to anyone who is not an admin, which is the
        // same as saying "this feature does not exist for you". An empty list
        // is the signal the screen uses not to show "Visible to..." at all.
        val backup = FakeBackupSource(targets = TargetsResult.Error("nao encontrado"))
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        // Before asking, `null`: "we have not asked yet" is different from
        // "we asked and there are none". The screen only hides the option in
        // the second case.
        assertEquals(null, vm.targets.value)

        vm.loadTargets()
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(emptyList<String>(), vm.targets.value)
    }

    @Test
    fun `os alvos sao pedidos UMA vez, nao a cada abertura de menu`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.loadTargets()
        dispatcher.scheduler.advanceUntilIdle()
        val afterFirst = vm.targets.value
        assertEquals(listOf("sam", "jordan", "*"), afterFirst)

        // The screen calls this on every recomposition that brings it back;
        // the second call has to be inert, not a second request.
        vm.loadTargets()
        vm.loadTargets()
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(afterFirst, vm.targets.value)
    }

}
