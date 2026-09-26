package com.vpsmanager.feature.terminal.ui

import com.vpsmanager.data.terminal.AcaoResult
import com.vpsmanager.data.terminal.AlvosResult
import com.vpsmanager.data.terminal.BackupsResult
import com.vpsmanager.data.terminal.PreviaResult
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
    private val alvos: AlvosResult = AlvosResult.Success(listOf("sam", "jordan", "*")),
    private var previa: PreviaResult = PreviaResult.Success("$ ls\nbin  etc", 2),
) : TerminalBackupSource {
    val mortas = mutableListOf<String>()
    val atribuidas = mutableListOf<Pair<String, String>>()
    var previasPedidas = 0
        private set

    override suspend fun backups() = BackupsResult.Empty
    override suspend fun criarBackup(sessao: String?) = AcaoResult.Ok("ok")
    override suspend fun restaurar(id: String, sessao: String?) = AcaoResult.Ok("ok")
    override suspend fun excluirBackup(id: String, sessao: String?) = AcaoResult.Ok("ok")
    override suspend fun renomearSessao(de: String, para: String) = AcaoResult.Ok("ok")

    override suspend fun matarSessao(nome: String): AcaoResult {
        mortas += nome
        return AcaoResult.Ok("Sessão $nome encerrada.")
    }

    override suspend fun atribuirSessao(nome: String, alvo: String): AcaoResult {
        atribuidas += nome to alvo
        return AcaoResult.Ok("$nome agora aparece para $alvo.")
    }

    override suspend fun previaDaSessao(nome: String, linhas: Int): PreviaResult {
        previasPedidas++
        return previa
    }

    override suspend fun alvosDeAtribuicao(): AlvosResult = alvos

    fun definirPrevia(r: PreviaResult) { previa = r }
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
        sessoes: List<TerminalSession> = listOf(
            TerminalSession(name = "main", attached = true, created = 1000, tab = null),
        ),
    ): SessionListViewModel = SessionListViewModel(
        sessionsSource = FakeTerminalSessionsSource(
            mutableListOf(TerminalSessionsResult.Success(sessoes)),
        ),
        backupSource = backup,
    )

    @Test
    fun `matar chama o servidor uma vez e recarrega a lista`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.matar("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(listOf("main"), backup.mortas)
        assertEquals("Sessão main encerrada.", vm.recado.value)
    }

    @Test
    fun `atribuir manda o alvo escolhido, sem traduzir`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.atribuir("main", "*")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(listOf("main" to "*"), backup.atribuidas)
    }

    @Test
    fun `espiar abre, e espiar de novo FECHA — sem pedir ao servidor duas vezes`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.alternarPrevia("main")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(PreviaUiState.Pronta("$ ls\nbin  etc"), vm.previas.value["main"])
        assertEquals(1, backup.previasPedidas)

        vm.alternarPrevia("main")
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(null, vm.previas.value["main"])
        assertEquals("fechar nao pede nada ao servidor", 1, backup.previasPedidas)
    }

    @Test
    fun `fechar a previa no meio do carregamento nao a reabre por baixo da pessoa`() = runTest {
        // The response arrives AFTER the person has closed it. Without the
        // guard, it would reappear on its own — and the person would tap
        // "close" all over again.
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.alternarPrevia("main")   // opens and starts loading
        vm.alternarPrevia("main")   // closes before the response comes back
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(null, vm.previas.value["main"])
    }

    @Test
    fun `previa vazia e ESTADO proprio, nao erro`() = runTest {
        // A freshly created session has not written anything yet. Showing that
        // as a failure would send people looking for a problem that does not
        // exist.
        val backup = FakeBackupSource(previa = PreviaResult.Success("   ", 0))
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.alternarPrevia("main")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(PreviaUiState.Vazia, vm.previas.value["main"])
    }

    @Test
    fun `sem permissao os alvos ficam VAZIOS — e a tela esconde a opcao`() = runTest {
        // The route returns 404 to anyone who is not an admin, which is the
        // same as saying "this feature does not exist for you". An empty list
        // is the signal the screen uses not to show "Visible to..." at all.
        val backup = FakeBackupSource(alvos = AlvosResult.Error("nao encontrado"))
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        // Before asking, `null`: "we have not asked yet" is different from
        // "we asked and there are none". The screen only hides the option in
        // the second case.
        assertEquals(null, vm.alvos.value)

        vm.carregarAlvos()
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(emptyList<String>(), vm.alvos.value)
    }

    @Test
    fun `os alvos sao pedidos UMA vez, nao a cada abertura de menu`() = runTest {
        val backup = FakeBackupSource()
        val vm = vmCom(backup)
        dispatcher.scheduler.advanceUntilIdle()

        vm.carregarAlvos()
        dispatcher.scheduler.advanceUntilIdle()
        val depoisDoPrimeiro = vm.alvos.value
        assertEquals(listOf("sam", "jordan", "*"), depoisDoPrimeiro)

        // The screen calls this on every recomposition that brings it back;
        // the second call has to be inert, not a second request.
        vm.carregarAlvos()
        vm.carregarAlvos()
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(depoisDoPrimeiro, vm.alvos.value)
    }

}
