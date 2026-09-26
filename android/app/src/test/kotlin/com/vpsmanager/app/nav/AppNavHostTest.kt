package com.vpsmanager.app.nav

import androidx.compose.runtime.getValue
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotDisplayed
import androidx.compose.ui.test.hasNoClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import androidx.navigation.compose.rememberNavController
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import com.vpsmanager.app.LaunchDestination
import com.vpsmanager.app.decideLaunchDestination
import com.vpsmanager.data.auth.InMemoryTokenStore
import com.vpsmanager.data.auth.RefreshOutcome
import com.vpsmanager.data.auth.SessionManager
import com.vpsmanager.data.auth.SessionRefresher
import com.vpsmanager.data.auth.SessionState
import com.vpsmanager.data.auth.SignOutSource
import com.vpsmanager.designsystem.VpsManagerTheme
import com.vpsmanager.feature.terminal.ui.BACK_DESCRIPTION
import com.vpsmanager.feature.terminal.ui.SessionListUiState
import com.vpsmanager.feature.terminal.ui.SessionListViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import com.vpsmanager.feature.admin.ADMIN_SECTION_AUTO
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.net.URLEncoder

/**
 * The whole shell: hamburger, drawer, navigation and deep link.
 *
 * What these tests protect, beyond "it draws": that swapping the
 * `NavigationBar` for the drawer did NOT cut the path by which a tapped
 * notification and a call answered on the lock screen enter the app — both
 * arrive as `pendingDeepLinkRoute`, and both have to keep landing on the
 * right route.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class AppNavHostTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var navController: NavHostController

    private fun renderShell(
        pendingDeepLinkRoute: String? = null,
        onSignOut: () -> Unit = {},
    ) {
        composeRule.setContent {
            VpsManagerTheme {
                navController = rememberNavController()
                AppNavHost(
                    pendingDeepLinkRoute = pendingDeepLinkRoute,
                    onSignOut = onSignOut,
                    navController = navController,
                )
            }
        }
        composeRule.waitForIdle()
    }

    private fun currentRoute(): String? = navController.currentBackStackEntry?.destination?.route

    @Test
    fun `a casca abre na Home, com o hamburguer descrito para leitor de tela`() {
        renderShell()

        assertEquals(AppDestination.Inicio.route, currentRoute())
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
    }

    @Test
    fun `o hamburguer abre a gaveta`() {
        renderShell()

        // A closed drawer is still COMPOSED, just off screen — which is why the
        // assertion is about being visible, not about existing. `assertExists`
        // here would pass with the drawer closed and would prove nothing.
        composeRule.onNodeWithText(AppDestination.Dev.label).assertIsNotDisplayed()

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        // Matches by the icon's description, not by the label: the current
        // destination's label appears TWICE on screen (in the drawer and as the
        // top bar's title), and the icon description is unique per destination.
        AppDestination.entries.forEach { destination ->
            composeRule.onNodeWithContentDescription(destination.iconDescription).assertIsDisplayed()
        }
        composeRule.onNodeWithContentDescription(SIGN_OUT_ICON_DESCRIPTION).assertIsDisplayed()
    }

    @Test
    fun `escolher um destino na gaveta navega e fecha a gaveta`() {
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        // Matches by the icon's description, and not by the label: the current
        // destination's label appears TWICE on screen (in the drawer and as the
        // bar's title).
        composeRule.onNodeWithContentDescription(AppDestination.Dev.iconDescription).performClick()
        composeRule.waitForIdle()

        // A CONCRETE route per parent. With a single parameterised registration,
        // the six parents were one destination to Navigation — and that is what
        // trapped the person inside one of them.
        assertEquals(rotaDaMae("dev"), currentRoute())
        // Drawer closed: the "Licences" label, which only appears inside it,
        // leaves the screen.
        composeRule.onNodeWithText(AppDestination.Configuracoes.label).assertIsNotDisplayed()
    }

    /**
     * The drawer opens Administration WITHOUT naming a section: the argument
     * is the [ADMIN_SECTION_AUTO] sentinel, and it is the server (via `GET
     * /screens`) that decides which section shows first. Before, a fixed
     * `scheduler.jobs` went out from here, and the server's other 24 screens
     * were unreachable.
     */
    @Test
    fun `uma mae abre a grade dela, e nao uma secao escolhida pelo cliente`() {
        // Administration with its thirty blocks stopped being the single door:
        // each family now has its own grid, and that is what leads to the SDUI
        // sections.
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription(AppDestination.Docker.iconDescription).performClick()
        composeRule.waitForIdle()

        assertEquals(rotaDaMae("docker"), currentRoute())
        // The parent names no section at all: it opens the GRID, and what picks
        // the section is the tap on the child. The `auto` sentinel died along
        // with the single thirty-block grid.
        assertNull(navController.currentBackStackEntry?.arguments?.getString("sectionId"))
    }

    /**
     * Licences left the prominent spot (it was next to Terminal in the bottom
     * bar) and moved to the "About" section, at the end of the drawer. Staying
     * REACHABLE is what this test pins down — "leaving the top level" must
     * not turn into "disappearing".
     */
    @Test
    fun `licencas continua alcancavel pela gaveta`() {
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription(AppDestination.Configuracoes.iconDescription).performClick()
        composeRule.waitForIdle()

        assertEquals(AppDestination.Configuracoes.route, currentRoute())
    }

    /**
     * From one parent to ANOTHER, straight through the drawer — without
     * pressing back first.
     *
     * This is the defect the owner reported ("I get stuck on the page"), and
     * it was not a click problem: with a single parameterised registration,
     * the six parents were one destination to Navigation, and the drawer's two
     * navigation options compare by DESTINATION. `launchSingleTop` swallowed
     * the tap and `restoreState` gave back the saved state of the parent you
     * were already in.
     *
     * The test walks the whole path — System, Docker, Operations, System again
     * — because the symptom only shows from the SECOND navigation onwards, and
     * a one-hop test would pass with the defect still standing.
     */
    @Test
    fun `da para trocar de mae pela gaveta sem apertar voltar`() {
        renderShell()

        listOf(
            AppDestination.Sistema,
            AppDestination.Docker,
            AppDestination.Operacoes,
            AppDestination.Sistema,
        ).forEach { mae ->
            navegarPelaGaveta(mae)
            assertEquals(
                "a gaveta nao saiu do lugar ao pedir ${mae.label}",
                mae.route,
                currentRoute(),
            )
        }
    }

    /**
     * Reaching the Terminal by two paths must NOT leave two alive on the stack.
     *
     * This was the defect the owner saw as every terminal line appearing twice,
     * overlapping: two stacked `terminal` destinations, each with its own
     * ViewModel, each asking for the scrollback replay and painting over the
     * other. The cause was the dock shortcut (and the parent's grid)
     * navigating with `launchSingleTop` alone, without the `popUpTo(start)`
     * the drawer used.
     *
     * The test walks the path the owner walked: in through the shortcut, out
     * to another parent, and in again through the shortcut.
     */
    @Test
    fun `entrar no Terminal duas vezes nao empilha dois terminais`() {
        renderShell()

        repeat(2) {
            composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
            composeRule.waitForIdle()
            composeRule.onNodeWithText(ATALHO_TERMINAL_LABEL).performClick()
            composeRule.waitForIdle()
            assertEquals(ROTA_TERMINAL, currentRoute())

            // From inside the Terminal there is no hamburger — there is "back".
            // That is the child-screen contract, and the path the person actually walks.
            composeRule.onNodeWithContentDescription(VOLTAR_DESCRIPTION).performClick()
            composeRule.waitForIdle()
        }

        // One `terminal` entry on the stack, never two.
        val quantos = navController.currentBackStack.value.count { it.destination.route == ROTA_TERMINAL }
        assertTrue("havia $quantos destinos de terminal empilhados, esperava no máximo 1", quantos <= 1)
    }

    @Test
    fun `notificacao tocada continua pousando na secao do agendador`() {
        renderShell(pendingDeepLinkRoute = resolveNotificationDeepLink("deploy_job", "job-1")?.navRoute)

        assertEquals("admin/{sectionId}", currentRoute())
        assertEquals(
            SCHEDULER_SECTION_ID,
            navController.currentBackStackEntry?.arguments?.getString("sectionId"),
        )
    }

    @Test
    fun `chamada atendida na lockscreen continua pousando na sala`() {
        renderShell(pendingDeepLinkRoute = resolveVideocallDeepLink("sala-7")?.navRoute)

        assertEquals("chamada/{roomId}", currentRoute())
        assertEquals("sala-7", navController.currentBackStackEntry?.arguments?.getString("roomId"))
    }

    /**
     * Detail screens bring their own bar (or are full-screen, like the call):
     * the shell's bar must not stack on top of them.
     */
    @Test
    fun `tela de detalhe nao ganha a barra da casca`() {
        renderShell(pendingDeepLinkRoute = resolveVideocallDeepLink("sala-7")?.navRoute)

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertDoesNotExist()
    }

    // ------------------------------------------------- one header per screen

    /** Opens the drawer and picks [destination]. */
    private fun navegarPelaGaveta(destination: AppDestination) {
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()
        // By the icon's DESCRIPTION, never by the label: the current
        // destination's label appears twice on screen — in the drawer item and in
        // the shell bar's title — and clicking by text would be ambiguous.
        composeRule.onNodeWithContentDescription(destination.iconDescription).performClick()
        composeRule.waitForIdle()
    }

    /**
     * A top-level destination has ONE header: the shell's.
     *
     * The real symptom of two headers was the title of the screen's own bar
     * appearing right below the shell's title — which is why the assertion is
     * about that specific title, and not about "count the `TopAppBar`s"
     * (Material's bar leaves no semantic mark that can be counted). Each row
     * of the table is a screen that used to bring its own bar: if someone
     * gives the bar back, its title reappears and this test falls over.
     *
     * The hamburger is counted too: it only exists on the shell's bar, so more
     * than one would mean more than one shell drawn.
     */
    @Test
    fun `destino de nivel principal nao empilha dois cabecalhos`() {
        // Terminal and Notifications stopped being drawer destinations: they
        // became CHILDREN (of Dev and of Settings). What answers for a top-level
        // bar now are the parents, and the risk of a double header became theirs
        // — which is why the test now sweeps the parents that have a grid.
        // Terminal and Notifications stopped being drawer destinations: they
        // became CHILDREN (of Dev and of Settings) and now bring their own bar
        // with "back", which is the child-screen contract. What answers for a
        // top-level bar are the parents — and their title IS the shell's, so
        // there is no "old title" left to look for. What remains is the proof
        // that matters: one hamburger, one shell.
        renderShell()
        DESTINOS_RENDERIZAVEIS_NA_JVM.forEach { destination ->
            navegarPelaGaveta(destination)

            assertEquals(
                "a casca desenhou mais de uma barra em ${destination.label}",
                1,
                composeRule.onAllNodesWithContentDescription(OPEN_DRAWER_DESCRIPTION).fetchSemanticsNodes().size,
            )
            // And no top-level bar shows "back": going back from a root has
            // nowhere to go.
            composeRule.onNodeWithContentDescription(VOLTAR_DESCRIPTION).assertDoesNotExist()
        }
    }

    /**
     * Defect 2: "Back" on a navigation root has nowhere to go back to.
     * Top level brings a hamburger; the back arrow is exclusive to the
     * detail screens. It walks ALL the drawer destinations, and not just
     * the two that used to show the button, so a new destination cannot
     * reintroduce it.
     */
    @Test
    fun `nenhum destino de nivel principal mostra voltar`() {
        renderShell()
        DESTINOS_RENDERIZAVEIS_NA_JVM.forEach { destination ->
            navegarPelaGaveta(destination)

            composeRule.onNodeWithText(BACK_DESCRIPTION).assertDoesNotExist()
            composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).assertDoesNotExist()
            composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
        }
    }

    /**
     * The converse, on a detail screen: a real back arrow — it exists, and
     * clicking it pops back to the destination you came from. It is the "back
     * means something" the root did not have.
     *
     * The detail chosen is the file editor, and not the terminal, because the
     * terminal runs a `withFrameNanos` loop while it is alive: under Compose's
     * test clock it draws frames without stopping and blows the JVM heap
     * before any assertion. The editor exercises exactly the same bar contract
     * (its own title + arrow + a "Save" action).
     */
    @Test
    fun `tela de detalhe tem voltar e ele volta`() {
        renderShell()
        val rotaDoEditor = "arquivos/edit/" + URLEncoder.encode("/etc/hosts", "UTF-8")
        navController.navigate(rotaDoEditor)
        composeRule.waitForIdle()

        assertEquals("arquivos/edit/{path}", currentRoute())
        // Detail bar: arrow yes, hamburger no.
        composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).assertExists()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertDoesNotExist()

        composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        // It really went back: it left the detail and the shell's hamburger reappeared.
        assertEquals(AppDestination.Inicio.route, currentRoute())
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
    }

    /**
     * The action that used to live on the Terminal's own bar ("Refresh") is
     * still reachable — now in the shell bar's `actions` — AND is still wired
     * to the ViewModel THAT DRAWS THE LIST, not to a second copy.
     *
     * The proof is that second part. The ViewModel read here is the one from
     * the destination's [androidx.navigation.NavBackStackEntry], that is,
     * exactly what `SessionListScreen` resolves when it calls `viewModel()`
     * inside itself. If the bar had created its own (a different owner, a
     * different key), the click would reload an invisible object and no new
     * `Loading` would show up in this one. Collecting with
     * [Dispatchers.Unconfined] records EVERY emission, so the transient
     * `Loading` is captured even if the load finishes right afterwards — with
     * no race against the server's answer (or failure).
     */
    @Test
    fun `atualizar continua alcancavel na barra do Terminal e recarrega a lista`() {
        // The action moved house along with the screen: the Terminal stopped
        // being a drawer destination and became a child of Dev, so "Refresh"
        // left the shell's bar and went to its OWN bar. The link to the
        // ViewModel that draws the list is the same, and that is what this test
        // goes on proving.
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithText(ATALHO_TERMINAL_LABEL).performClick()
        composeRule.waitForIdle()

        val entrada = navController.getBackStackEntry(ROTA_TERMINAL)
        val doConteudo = ViewModelProvider(entrada)[SessionListViewModel::class.java]
        val emissoes = mutableListOf<SessionListUiState>()
        val coleta = CoroutineScope(Dispatchers.Unconfined).launch {
            doConteudo.uiState.collect { emissoes += it }
        }

        try {
            val carregamentosAntes = emissoes.count { it is SessionListUiState.Loading }
            composeRule.onNodeWithText(REFRESH_ACTION_LABEL).assertExists()
            composeRule.onNodeWithText(REFRESH_ACTION_LABEL).performClick()
            composeRule.waitForIdle()

            assertTrue(
                "clicar em Atualizar não recarregou o ViewModel da lista",
                emissoes.count { it is SessionListUiState.Loading } > carregamentosAntes,
            )
        } finally {
            coleta.cancel()
        }
    }

    // ---------------------------------------------------------------- sair

    private class NeverCalledRefresher : SessionRefresher {
        override suspend fun refresh(refreshToken: String): RefreshOutcome =
            throw AssertionError("sair não renova sessão")
    }

    /** Mirrors `MainActivity`'s `when`: a live session shows the shell, a dead session shows the sign-in. */
    @Composable
    private fun SessionGate(session: SessionManager, signOut: FakeSignOut) {
        val state by session.state.collectAsStateWithLifecycle()
        when (decideLaunchDestination(hasServerConfigured = true, hasSession = state is SessionState.SignedIn)) {
            LaunchDestination.Home -> AppNavHost(
                onSignOut = { signOut.blockingSignOut() },
                navController = rememberNavController(),
            )
            LaunchDestination.AuthGate -> Text(text = TELA_DE_ENTRADA)
        }
    }

    @Test
    fun `sair derruba a sessao e devolve o app para a tela de entrada`() {
        val session = SessionManager(
            tokenStore = InMemoryTokenStore(),
            refresher = NeverCalledRefresher(),
            publishAccessToken = {},
        ).apply { establish("access-1", "refresh-1", 900) }
        val signOut = FakeSignOut(session)

        composeRule.setContent { VpsManagerTheme { SessionGate(session, signOut) } }
        composeRule.waitForIdle()

        // Signed in: the shell is up and the sign-in is not.
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
        composeRule.onNodeWithText(TELA_DE_ENTRADA).assertDoesNotExist()

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithText(SIGN_OUT_LABEL).performClick()
        composeRule.waitForIdle()

        assertTrue("a sessão continuou de pé", session.state.value is SessionState.SignedOut)
        assertNull("o token não foi descartado", session.currentAccessToken())
        composeRule.onNodeWithText(TELA_DE_ENTRADA).assertExists()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertDoesNotExist()
    }

    /**
     * The real one (`SignOutRepository`, covered in `:data`) is `suspend`
     * because it revokes on the BFF before wiping the tokens. Here only the
     * local effect matters, and synchronous keeps the assertion deterministic.
     */
    private class FakeSignOut(private val session: SessionManager) : SignOutSource {
        override suspend fun signOut() = blockingSignOut()
        fun blockingSignOut() {
            session.signOut()
        }
    }

    private companion object {
        const val TELA_DE_ENTRADA = "Entrar no VPS Manager"

        /**
         * The drawer destinations that compose under Robolectric with no device.
         * Apps is left out, since its grid leads to Call and WhatsApp (which
         * build WebRTC/WebView, and the JVM has neither). The "top level does
         * not show back" rule holds for it just the same — the Apps grid draws
         * no more of a bar of its own than the others.
         */
        val DESTINOS_RENDERIZAVEIS_NA_JVM = listOf(
            AppDestination.Inicio,
            AppDestination.Sistema,
            AppDestination.Docker,
            AppDestination.Dev,
            AppDestination.Seguranca,
            AppDestination.Operacoes,
            AppDestination.Configuracoes,
        )
    }
}
