package dev.servercontrolpanel.feature.auth

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
import dev.servercontrolpanel.data.dashboard.DashboardResult
import dev.servercontrolpanel.data.dashboard.DashboardTarget
import dev.servercontrolpanel.designsystem.PanelTheme
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Home dashboard under Robolectric: loading, error, content and calm
 * states, thresholds, and each card's navigation.
 *
 * The virtual screen is tall so the lower cards of the `LazyColumn` get composed.
 */
@RunWith(RobolectricTestRunner::class)
@Config(qualifiers = "w411dp-h2200dp")
class HomeScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    @Test
    fun `loading shows a skeleton with card titles, not a blank screen`() {
        composeRule.dashboard(HomeUiState.Loading)

        composeRule.onNodeWithText("Loading the dashboard…").assertIsDisplayed()
        // "Quick actions" needs no server data, so it is a stable landmark.
        composeRule.onNodeWithText("Quick actions").assertIsDisplayed()
    }

    @Test
    fun `a hard error says what happened and what to do, and the button reloads`() {
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
    fun `a failed reload keeps the dashboard and warns the numbers are old`() {
        composeRule.dashboard(
            HomeUiState.Success(
                snapshot = snapshotReal(),
                staleError = "The server is unavailable right now.",
            ),
        )

        composeRule.onNodeWithText("The numbers below are from 07:08:22").assertIsDisplayed()
        composeRule.onNodeWithText("The server is unavailable right now.").assertIsDisplayed()
        composeRule.onNodeWithText("Quick actions").assertIsDisplayed()
    }

    @Test
    fun `the real machine does not look all fine, swap and load rise to the top`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        // The attention card shows even with no alerts and health_ok = true.
        composeRule.onNodeWithText("3 things need attention").assertIsDisplayed()
        // The crossed signal appears with the sentence explaining its threshold.
        composeRule.onAllNodesWithText("Swap").assertCountAtLeast(1)
        // CPU steal is not an alert (nothing can be done inside the VM); it stays in the grid only.
        composeRule.onNodeWithText("CPU steal  37%").assertDoesNotExist()
        composeRule.onNodeWithText(
            "8.0 GiB of 8.0 GiB — no room left to page; a memory spike goes straight to the OOM killer",
        ).assertExists()
    }

    @Test
    fun `a bad value has a text label, not only a color`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        // Color alone fails color-blind users, sunlight and screen readers.
        composeRule.onAllNodesWithText("WARNING").assertCountAtLeast(1)
    }

    @Test
    fun `full swap with RAM at its limit shows as CRITICAL`() {
        val ops = opsReal(systemReal(memUsedPercent = 97.0))
        composeRule.dashboard(HomeUiState.Success(snapshotReal(ops = ops)))

        composeRule.onAllNodesWithText("CRITICAL").assertCountAtLeast(1)
        composeRule.onAllNodesWithText(
            "8.0 GiB of 8.0 GiB with RAM at its limit — nowhere to page to and nothing left to allocate",
        ).assertCountAtLeast(1)
    }

    @Test
    fun `on a calm machine the attention card disappears, with no good-news message`() {
        // Silence is the good news: no warning card at all.
        composeRule.dashboard(HomeUiState.Success(calmSnapshot()))

        composeRule.onAllNodesWithText("No alerts firing right now.").assertCountEquals(0)
        composeRule.onAllNodesWithText("1 thing needs attention").assertCountEquals(0)
        composeRule.onAllNodesWithText("2 things need attention").assertCountEquals(0)
    }

    @Test
    fun `a server without the system block says resources are missing instead of showing zero`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal(ops = opsReal(system = null))))

        composeRule.scrollTo("unavailable")
        composeRule.onNodeWithText(
            "This server does not expose CPU, memory and disk in /ops/status. " +
                "Update Server Control Panel to see resources here.",
        ).assertIsDisplayed()
    }

    @Test
    fun `the grid shows the initial tiles when nothing has been chosen yet`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        // `onAllNodes`: a crossed signal appears both in the grid and on the attention card.
        composeRule.onNodeWithText("Dashboard").assertExists()
        composeRule.onAllNodesWithText("CPU").assertCountAtLeast(1)
        composeRule.onAllNodesWithText("MEMORY").assertCountAtLeast(1)
        composeRule.onNodeWithText("Customize").assertExists()
    }

    /** The dashboard must never hide a fire: a CRITICAL tile shows even if not chosen. */
    @Test
    fun `a CRITICAL tile appears in the grid even if not chosen`() {
        // Memory at 97% makes swap CRITICAL (see swapSignal).
        val ops = opsReal(systemReal(memUsedPercent = 97.0))
        composeRule.dashboard(HomeUiState.Success(snapshotReal(ops = ops)))

        // Swap is not in INITIAL_TILES, so only the critical rule can bring it in.
        composeRule.onAllNodesWithText("SWAP").assertCountAtLeast(1)
    }

    @Test
    fun `the footer shows identity with admin and uptime`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal()))

        composeRule.scrollTo("tester · admin")
        composeRule.onNodeWithText("tester · admin").assertIsDisplayed()
        composeRule.onNodeWithText("test@northwind.example").assertExists()
        composeRule.onNodeWithText("host01 · ubuntu 24.04").assertExists()
        composeRule.onNodeWithText(
            "up for 18d 13h 19m · server clock 2026-09-06T07:08:22Z",
        ).assertExists()
    }

    @Test
    fun `a missing identity does not break the dashboard and becomes a footer line`() {
        composeRule.dashboard(HomeUiState.Success(snapshotReal(identity = null)))

        composeRule.scrollTo("identity unavailable")
        composeRule.onNodeWithText(
            "The server did not return this session's identity. The dashboard above is still valid.",
        ).assertExists()
    }

    @Test
    fun `server clock drift is reported`() {
        // Device 10 minutes ahead of the server.
        val snapshot = snapshotReal(fetchedAtEpochMs = (1_788_678_502L + 600) * 1_000)
        composeRule.dashboard(HomeUiState.Success(snapshot))

        composeRule.scrollTo("Server clock 600 s behind the device")
        composeRule.onNodeWithText("Server clock 600 s behind the device").assertExists()
    }

    @Test
    fun `tapping a top signal opens its screen`() {
        var target: DashboardTarget? = null
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { target = it })

        // Index 0 is the attention card's row, at the top.
        composeRule.onAllNodesWithText("Swap")[0].performClick()
        assertEquals(DashboardTarget.PROCESSES, target)
    }

    @Test
    fun `tapping the rolled-back deploy opens the deploys screen`() {
        var target: DashboardTarget? = null
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { target = it })

        composeRule.onNodeWithText("Deploy hello").performClick()
        assertEquals(DashboardTarget.DEPLOYS, target)
    }

    @Test
    fun `tapping the queue opens the job queue`() {
        var target: DashboardTarget? = null
        composeRule.dashboard(HomeUiState.Success(snapshotReal()), onTarget = { target = it })

        // Uppercase because `TileGrid` renders labels uppercased.
        composeRule.scrollTo("QUEUE")
        composeRule.onNodeWithText("QUEUE").performClick()
        assertEquals(DashboardTarget.QUEUE, target)
    }

    @Test
    fun `quick actions open each destination`() {
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
    fun `every emitted section exists on the server, no destination is dangling`() {
        // The 25 SDUI screens served by the BFF; any other id would open a missing-section screen.
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
            assert(id in serverSections) { "target $target points at missing section '$id'" }
        }
    }

    @Test
    fun `the whole screen starts from the ViewModel and reaches the dashboard`() {
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        composeRule.setContent {
            PanelTheme {
                // Auto-refresh off: its infinite `delay` would keep `waitForIdle` waiting forever.
                HomeScreen(viewModel = vm, autoRefreshMillis = 0)
            }
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("3 things need attention").assertIsDisplayed()
    }

    @Test
    fun `the screen maps targets to a section or the terminal without knowing routes`() {
        var section: String? = null
        var terminal = false
        val vm = HomeViewModel(FakeDashboardSource { DashboardResult.Success(snapshotReal()) })
        composeRule.setContent {
            PanelTheme {
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
    fun `a first-load error shows the error screen, and retry brings the dashboard`() {
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
        composeRule.setContent { PanelTheme { HomeScreen(viewModel = vm, autoRefreshMillis = 0) } }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Can't reach the server").assertIsDisplayed()
        composeRule.onNodeWithText("  Try again").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Quick actions").assertIsDisplayed()
    }

    @Test
    fun `loading never flashes empty before the fetch finishes`() {
        val vm = HomeViewModel(FakeDashboardSource { awaitCancellation() })
        composeRule.setContent { PanelTheme { HomeScreen(viewModel = vm, autoRefreshMillis = 0) } }

        composeRule.onNodeWithText("Loading the dashboard…").assertIsDisplayed()
        composeRule.onAllNodesWithText("9 subsystems · all ok").assertCountEquals(0)
    }
}

/** Composes the content only, without a ViewModel; every state is a parameter. */
private fun ComposeContentTestRule.dashboard(
    state: HomeUiState,
    onRetry: () -> Unit = {},
    onRefresh: () -> Unit = {},
    onTarget: (DashboardTarget) -> Unit = {},
) {
    setContent {
        PanelTheme {
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
    assert(actual >= min) { "expected at least $min nodes, found $actual" }
}
