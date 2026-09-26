package com.vpsmanager.feature.auth

import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import com.vpsmanager.data.dashboard.DashboardResult
import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.designsystem.VpsManagerTheme
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Home dashboard under Robolectric, in loading / error / content /
 * silence, plus the threshold behaviour and the navigation of each card.
 *
 * The screen is tall (six cards in a `LazyColumn`), so the test's virtual
 * screen is declared large: without it, the bottom cards simply never get
 * composed and the test would be measuring the emulator's height instead
 * of the content.
 */
@RunWith(RobolectricTestRunner::class)
@Config(qualifiers = "w411dp-h2200dp")
class HomeScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    // ── states ──────────────────────────────────────────────────────────────

    @Test
    fun `carregando mostra esqueleto com os titulos dos cartoes, nao tela em branco`() {
        composeRule.dashboard(HomeUiState.Loading)

        composeRule.onNodeWithText("Loading the dashboard…").assertIsDisplayed()
        // The "the skeleton has TITLES, it is not a blank screen" landmark
        // kept moving as cards left the Home — health, resources and now the
        // "Agora" one. "Acoes rapidas" is what is left, and it depends on no
        // server data at all, which is exactly what makes it a good landmark.
        composeRule.onNodeWithText("Quick actions").assertIsDisplayed()
    }

    @Test
    fun `erro duro diz o que houve E o que fazer, e o botao recarrega`() {
        var reloaded = false
        composeRule.dashboard(
            state = HomeUiState.Error("Connection failed. Check your network and try again."),
            onRetry = { reloaded = true },
        )

        composeRule.onNodeWithText("Can't reach the server").assertIsDisplayed()
        composeRule.onNodeWithText("Connection failed. Check your network and try again.").assertIsDisplayed()
        composeRule.onNodeWithText(
            "Check the device's network and the server address in Settings. " +
                "The dashboard comes back on its own as soon as the connection responds.",
        ).assertIsDisplayed()

        composeRule.onNodeWithText("  Try again").performClick()
        assertEquals(true, reloaded)
    }

    @Test
    fun `recarga que falha preserva o painel e avisa que o numero e velho`() {
        composeRule.dashboard(
            HomeUiState.Success(
                snapshot = snapshotReal(),
                staleError = "The server is unavailable right now.",
            ),
        )

        composeRule.onNodeWithText("The numbers below are from 07:08:22").assertIsDisplayed()
        composeRule.onNodeWithText("The server is unavailable right now.").assertIsDisplayed()
        // and the old content is still there
        composeRule.onNodeWithText("Quick actions").assertIsDisplayed()
    }

    // ── the test that gives the work its name ───────────────────────────────

    @Test
    fun `a maquina real NAO aparece como tudo bem — swap e carga sobem para o topo`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        // The attention card exists, even with alerts empty and health_ok = true.
        composeRule.onNodeWithText("3 things need attention").assertIsDisplayed()
        // The crossed signal appears on the attention card TOGETHER with the
        // sentence that explains the threshold — that is where the explanation
        // matters, once the threshold has been crossed. (It used to appear
        // twice, because there was a resources card as well; the block grid
        // replaced it.)
        composeRule.onAllNodesWithText("Swap").assertCountAtLeast(1)
        // "CPU roubada" NO LONGER appears in the attention card: it is
        // information, not an alert, because no action is possible from inside
        // the VM. Its number stays visible in the grid — what went away was
        // the shouting.
        composeRule.onNodeWithText("CPU steal  37%").assertDoesNotExist()
        composeRule.onNodeWithText(
            "8.0 GiB of 8.0 GiB — no room left to page; a memory spike goes straight to the OOM killer",
        ).assertExists()
    }

    @Test
    fun `o valor ruim aparece com etiqueta textual, nao so com cor`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        // Colour on its own is no use to a colour-blind eye, to sunlight on
        // the screen, or to a screen reader.
        composeRule.onAllNodesWithText("WARNING").assertCountAtLeast(1)
    }

    @Test
    fun `swap somado a RAM no limite vira CRITICO na tela`() {
        val ops = opsReal(systemReal(memUsedPercent = 97.0))
        composeRule.dashboard(HomeUiState.Success(snapshotReal(ops = ops)))

        composeRule.onAllNodesWithText("CRITICAL").assertCountAtLeast(1)
        composeRule.onAllNodesWithText(
            "8.0 GiB of 8.0 GiB with RAM at its limit — nowhere to page to and nothing left to allocate",
        ).assertCountAtLeast(1)
    }

    @Test
    fun `numa maquina calma o cartao de atencao SOME — sem boa noticia no lugar`() {
        // The "good news" ("Nenhum alerta disparando") belonged to the health
        // card, which left the Home at the owner's request. Silence becomes the
        // good news itself: the absence of a warning is the cheapest way to say
        // everything is fine, and it does not spend the first screen saying it.
        composeRule.dashboard(HomeUiState.Success(calmSnapshot()))

        composeRule.onAllNodesWithText("Nenhum alerta disparando.").assertCountEquals(0)
        composeRule.onAllNodesWithText("1 thing needs attention").assertCountEquals(0)
        composeRule.onAllNodesWithText("2 things need attention").assertCountEquals(0)
    }

    // ── aggregate health: LEFT THE HOME ─────────────────────────────────────
    //
    // The two tests that lived here covered the health card, removed from the
    // first screen at the owner's request. The behaviour was not lost — health
    // per subsystem still lives in its own section under Administration, and
    // that is where it should be tested if it starts to matter again.



    // ── "agora" and resources ───────────────────────────────────────────────

    // THE TEST FOR THE "AGORA" CARD LEFT ALONG WITH THE CARD (at the owner's
    // request). Queue, deploy and scheduled are still on the Home through the
    // block grid, and that is where they are tested — see `DashboardTilesTest`
    // and the queue tap test, just below.

    // The "I could not find out" was said TWICE by the "Agora" card (deploy
    // and scheduled), and the card is gone. The rule it protected — a failed
    // call NEVER turns into zero, because zero is a statement — still holds and
    // is still tested where the information now lives: `DashboardTilesTest`,
    // which exercises deploys=null and scheduled=null straight into the
    // blocks' constructor.

    @Test
    fun `servidor sem o bloco system diz que os recursos faltam, sem inventar zero`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal(ops = opsReal(system = null))))

        composeRule.scrollTo("unavailable")
        composeRule.onNodeWithText(
            "This server does not expose CPU, memory and disk in /ops/status. " +
                "Update Server Control Panel to see resources here.",
        ).assertIsDisplayed()
    }

    @Test
    fun `a grade mostra os blocos iniciais quando ninguem escolheu nada ainda`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        // A dashboard that starts out empty forces you to assemble it before
        // seeing any value. These four are the questions every operator asks.
        //
        // `onAllNodes` and not `onNode`: a signal past its threshold appears
        // in the GRID (the number) and on the attention card (the explanation),
        // and both occurrences are correct — the grid says what, the card says
        // why.
        composeRule.onNodeWithText("Dashboard").assertExists()
        composeRule.onAllNodesWithText("CPU").assertCountAtLeast(1)
        composeRule.onAllNodesWithText("MEMORY").assertCountAtLeast(1)
        composeRule.onNodeWithText("Customize").assertExists()
    }

    /**
     * THE RULE THAT MATTERS MOST IN THE GRID: a dashboard cannot hide a fire.
     *
     * The real machine behind this snapshot has swap with no headroom and
     * stolen CPU — none of that is among the initial blocks. If the person's
     * choice were absolute, the screen would stay green with two thresholds
     * crossed, and a dashboard that can omit the one thing that is wrong is
     * worse than no dashboard at all, because it is consulted with confidence.
     */
    @Test
    fun `bloco CRITICO aparece na grade mesmo sem ter sido escolhido`() {
        // memory at 97% makes swap CRITICAL (see swapSignal): no headroom to
        // page out AND no RAM to allocate.
        val ops = opsReal(systemReal(memUsedPercent = 97.0))
        composeRule.dashboard(HomeUiState.Success(snapshotReal(ops = ops)))

        // "Swap" is not in INITIAL_TILES — if it shows up, it was the
        // critical rule that brought it in.
        composeRule.onAllNodesWithText("SWAP").assertCountAtLeast(1)
    }

    // ── session in the footer ───────────────────────────────────────────────

    @Test
    fun `identidade desceu para o rodape, com admin e uptime`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        composeRule.scrollTo("teste · admin")
        composeRule.onNodeWithText("teste · admin").assertIsDisplayed()
        composeRule.onNodeWithText("test@northwind.example").assertExists()
        composeRule.onNodeWithText("host01 · ubuntu 24.04").assertExists()
        composeRule.onNodeWithText(
            "up for 18d 13h 19m · server clock 2026-09-06T07:08:22Z",
        ).assertExists()
    }

    @Test
    fun `identidade ausente nao derruba o painel — vira uma linha do rodape`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal(identity = null)))

        composeRule.scrollTo("identity unavailable")
        composeRule.onNodeWithText(
            "The server did not return this session's identity. The dashboard above is still valid.",
        ).assertExists()
    }

    @Test
    fun `relogio do servidor fora de sincronia e denunciado`() {
        // Device 10 minutes ahead of the server.
        val snapshot = snapshotReal(fetchedAtEpochMs = (1_788_678_502L + 600) * 1_000)
        composeRule.dashboard(HomeUiState.Success(snapshot))

        composeRule.scrollTo("Server clock 600 s behind the device")
        composeRule.onNodeWithText("Server clock 600 s behind the device").assertExists()
    }

    // ── navigation: no card is a dead end ───────────────────────────────────

    @Test
    fun `tocar um sinal do topo leva a tela dele`() {
        var target: DashboardTarget? = null
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { target = it })

        // [0] = the attention card's row, which is the one at the top.
        composeRule.onAllNodesWithText("Swap")[0].performClick()
        assertEquals(DashboardTarget.PROCESSES, target)
    }

    @Test
    fun `tocar o deploy revertido leva a tela de deploys`() {
        var target: DashboardTarget? = null
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { target = it })

        composeRule.onNodeWithText("Deploy hello").performClick()
        assertEquals(DashboardTarget.DEPLOYS, target)
    }

    @Test
    fun `tocar a fila leva a fila de jobs`() {
        var target: DashboardTarget? = null
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { target = it })

        // Through the grid BLOCK, no longer through the "Agora" card's row:
        // the card is gone, the path to the queue is not. And this is what the
        // test protected — the destination, not the card.
        // UPPERCASE: `TileGrid` draws `bloco.rotulo.uppercase()`.
        composeRule.scrollTo("QUEUE")
        composeRule.onNodeWithText("QUEUE").performClick()
        assertEquals(DashboardTarget.QUEUE, target)
    }

    @Test
    fun `as acoes rapidas levam a cada destino`() {
        val targets = mutableListOf<DashboardTarget>()
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { targets += it })

        composeRule.scrollTo("Quick actions")
        listOf("Terminal", "Docker", "Deploys", "Audit log").forEach {
            composeRule.onNodeWithText(it).performClick()
        }

        assertEquals(
            listOf(
                DashboardTarget.TERMINAL,
                DashboardTarget.DOCKER,
                DashboardTarget.DEPLOYS,
                DashboardTarget.AUDITORIA,
            ),
            targets,
        )
    }

    @Test
    fun `toda secao emitida existe no servidor — nenhum destino aponta para o vazio`() {
        // The 25 SDUI screens served by the BFF; a destination outside this
        // list would open "Esta seção não existe" on the phone.
        val serverSections = setOf(
            "alerts.rules", "deploy.apps", "docker.compose", "docker.containers", "docker.images",
            "docker.networks", "docker.prune", "docker.volumes", "ai.settings", "jira.issues",
            "queue.jobs", "scheduler.jobs", "security.adguard", "security.audit", "security.devices",
            "security.economia", "security.secrets", "security.sessions", "security.ufw",
            "security.users", "system.history", "system.metrics", "system.ports",
            "system.processes", "system.systemd",
        )
        DashboardTarget.entries.forEach { target ->
            val id = target.sectionId ?: return@forEach
            assert(id in serverSections) { "destino $target aponta para a seção inexistente '$id'" }
        }
    }

    // ── wiring of the whole screen, with ViewModel ──────────────────────────

    @Test
    fun `a tela inteira sobe a partir do ViewModel e chega ao painel`() {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        composeRule.setContent {
            VpsManagerTheme {
                // Auto-refresh off: an infinite `delay` in the composition never lets
                // Compose go idle and `waitForIdle` would wait for ever.
                HomeScreen(viewModel = vm, autoRefreshMillis = 0)
            }
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("3 things need attention").assertIsDisplayed()
    }

    @Test
    fun `a tela traduz o destino em secao e em terminal, sem conhecer rota`() {
        var section: String? = null
        var terminal = false
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        composeRule.setContent {
            VpsManagerTheme {
                HomeScreen(
                    viewModel = vm,
                    onOpenSection = { section = it },
                    onOpenTerminal = { terminal = true },
                    autoRefreshMillis = 0,
                )
            }
        }
        composeRule.waitForIdle()

        composeRule.onAllNodesWithText("Swap")[0].performClick()
        assertEquals("system.processes", section)

        composeRule.scrollTo("Quick actions")
        composeRule.onNodeWithText("Terminal").performClick()
        assertEquals(true, terminal)
    }

    @Test
    fun `erro na primeira carga vira tela de erro, e nova tentativa traz o painel`() {
        var attempts = 0
        val vm = HomeViewModel(
            FakeDashboardSource {
                attempts += 1
                if (attempts == 1) {
                    DashboardResult.Error("The server is unavailable right now.")
                } else {
                    DashboardResult.Success(snapshotReal())
                }
            },
        )
        composeRule.setContent { VpsManagerTheme { HomeScreen(viewModel = vm, autoRefreshMillis = 0) } }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Can't reach the server").assertIsDisplayed()
        composeRule.onNodeWithText("  Try again").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Quick actions").assertIsDisplayed()
    }

    @Test
    fun `carregando nao pisca vazio antes de a busca terminar`() {
        val vm = HomeViewModel(FakeDashboardSource { awaitCancellation() })
        composeRule.setContent { VpsManagerTheme { HomeScreen(viewModel = vm, autoRefreshMillis = 0) } }

        composeRule.onNodeWithText("Loading the dashboard…").assertIsDisplayed()
        composeRule.onAllNodesWithText("9 subsystems · all ok").assertCountEquals(0)
    }
}

/** Composes the content only, without a ViewModel — every state is a parameter. */
private fun ComposeContentTestRule.dashboard(
    state: HomeUiState,
    onRetry: () -> Unit = {},
    onRefresh: () -> Unit = {},
    onTarget: (DashboardTarget) -> Unit = {},
) {
    setContent {
        VpsManagerTheme {
            HomeDashboard(state = state, onRetry = onRetry, onRefresh = onRefresh, onTarget = onTarget)
        }
    }
    waitForIdle()
}

/** Scrolls the `LazyColumn` down to the item containing [text]. */
private fun ComposeContentTestRule.scrollTo(text: String) {
    onNode(hasScrollAction()).performScrollToNode(hasText(text, substring = true))
    waitForIdle()
}

private fun androidx.compose.ui.test.SemanticsNodeInteractionCollection.assertCountAtLeast(min: Int) {
    val actual = fetchSemanticsNodes().size
    assert(actual >= min) { "esperava ao menos $min nós, achei $actual" }
}
