package dev.servercontrolpanel.app.nav

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
import dev.servercontrolpanel.app.LaunchDestination
import dev.servercontrolpanel.app.decideLaunchDestination
import dev.servercontrolpanel.data.auth.InMemoryTokenStore
import dev.servercontrolpanel.data.auth.RefreshOutcome
import dev.servercontrolpanel.data.auth.SessionManager
import dev.servercontrolpanel.data.auth.SessionRefresher
import dev.servercontrolpanel.data.auth.SessionState
import dev.servercontrolpanel.data.auth.SignOutSource
import dev.servercontrolpanel.designsystem.PanelTheme
import dev.servercontrolpanel.feature.terminal.ui.BACK_DESCRIPTION
import dev.servercontrolpanel.feature.terminal.ui.SessionListUiState
import dev.servercontrolpanel.feature.terminal.ui.SessionListViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import dev.servercontrolpanel.feature.admin.ADMIN_SECTION_AUTO
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.net.URLEncoder

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
            PanelTheme {
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
    fun `the shell opens on Home with the hamburger described for screen readers`() {
        renderShell()

        assertEquals(AppDestination.Home.route, currentRoute())
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
    }

    @Test
    fun `the hamburger opens the drawer`() {
        renderShell()

        composeRule.onNodeWithText(AppDestination.Dev.label).assertIsNotDisplayed()

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        AppDestination.entries.forEach { destination ->
            composeRule.onNodeWithContentDescription(destination.iconDescription).assertIsDisplayed()
        }
        composeRule.onNodeWithContentDescription(SIGN_OUT_ICON_DESCRIPTION).assertIsDisplayed()
    }

    @Test
    fun `picking a drawer destination navigates and closes the drawer`() {
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription(AppDestination.Dev.iconDescription).performClick()
        composeRule.waitForIdle()

        assertEquals(parentRoute("dev"), currentRoute())
        composeRule.onNodeWithText(AppDestination.Settings.label).assertIsNotDisplayed()
    }

    @Test
    fun `a parent opens its grid, not a section chosen by the client`() {
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription(AppDestination.Docker.iconDescription).performClick()
        composeRule.waitForIdle()

        assertEquals(parentRoute("docker"), currentRoute())
        assertNull(navController.currentBackStackEntry?.arguments?.getString("sectionId"))
    }

    @Test
    fun `licences stay reachable from the drawer`() {
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription(AppDestination.Settings.iconDescription).performClick()
        composeRule.waitForIdle()

        assertEquals(AppDestination.Settings.route, currentRoute())
    }

    @Test
    fun `the drawer switches between parents without pressing back`() {
        renderShell()

        listOf(
            AppDestination.System,
            AppDestination.Docker,
            AppDestination.Operations,
            AppDestination.System,
        ).forEach { parent ->
            navigateFromDrawer(parent)
            assertEquals(
                "the drawer did not navigate when asked for ${parent.label}",
                parent.route,
                currentRoute(),
            )
        }
    }

    @Test
    fun `entering the Terminal twice does not stack two terminals`() {
        renderShell()

        repeat(2) {
            composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
            composeRule.waitForIdle()
            composeRule.onNodeWithText(TERMINAL_SHORTCUT_LABEL).performClick()
            composeRule.waitForIdle()
            assertEquals(ROUTE_TERMINAL, currentRoute())

            composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).performClick()
            composeRule.waitForIdle()
        }

        val howMany = navController.currentBackStack.value.count { it.destination.route == ROUTE_TERMINAL }
        assertTrue("found $howMany stacked terminal destinations, expected at most 1", howMany <= 1)
    }

    @Test
    fun `a tapped notification still lands on the scheduler section`() {
        renderShell(pendingDeepLinkRoute = resolveNotificationDeepLink("deploy_job", "job-1")?.navRoute)

        assertEquals("admin/{sectionId}", currentRoute())
        assertEquals(
            SCHEDULER_SECTION_ID,
            navController.currentBackStackEntry?.arguments?.getString("sectionId"),
        )
    }

    @Test
    fun `a call answered on the lock screen still lands in the room`() {
        renderShell(pendingDeepLinkRoute = resolveVideocallDeepLink("room-7")?.navRoute)

        assertEquals("call/{roomId}", currentRoute())
        assertEquals("room-7", navController.currentBackStackEntry?.arguments?.getString("roomId"))
    }

    @Test
    fun `a detail screen does not get the shell bar`() {
        renderShell(pendingDeepLinkRoute = resolveVideocallDeepLink("room-7")?.navRoute)

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertDoesNotExist()
    }

    private fun navigateFromDrawer(destination: AppDestination) {
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithContentDescription(destination.iconDescription).performClick()
        composeRule.waitForIdle()
    }

    @Test
    fun `a top-level destination does not stack two headers`() {
        renderShell()
        JVM_RENDERABLE_DESTINATIONS.forEach { destination ->
            navigateFromDrawer(destination)

            assertEquals(
                "the shell drew more than one bar on ${destination.label}",
                1,
                composeRule.onAllNodesWithContentDescription(OPEN_DRAWER_DESCRIPTION).fetchSemanticsNodes().size,
            )
            composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).assertDoesNotExist()
        }
    }

    @Test
    fun `no top-level destination shows back`() {
        renderShell()
        JVM_RENDERABLE_DESTINATIONS.forEach { destination ->
            navigateFromDrawer(destination)

            composeRule.onNodeWithText(BACK_DESCRIPTION).assertDoesNotExist()
            composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).assertDoesNotExist()
            composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
        }
    }

    @Test
    fun `a detail screen has back and it goes back`() {
        renderShell()
        val editorRoute = "files/edit/" + URLEncoder.encode("/etc/hosts", "UTF-8")
        navController.navigate(editorRoute)
        composeRule.waitForIdle()

        assertEquals("files/edit/{path}", currentRoute())
        composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).assertExists()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertDoesNotExist()

        composeRule.onNodeWithContentDescription(BACK_DESCRIPTION).performClick()
        composeRule.waitForIdle()

        assertEquals(AppDestination.Home.route, currentRoute())
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
    }

    @Test
    fun `refresh stays reachable on the Terminal bar and reloads the list`() {
        renderShell()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithText(TERMINAL_SHORTCUT_LABEL).performClick()
        composeRule.waitForIdle()

        val entry = navController.getBackStackEntry(ROUTE_TERMINAL)
        val contentViewModel = ViewModelProvider(entry)[SessionListViewModel::class.java]
        val emissions = mutableListOf<SessionListUiState>()
        val collector = CoroutineScope(Dispatchers.Unconfined).launch {
            contentViewModel.uiState.collect { emissions += it }
        }

        try {
            val loadsBefore = emissions.count { it is SessionListUiState.Loading }
            composeRule.onNodeWithText(REFRESH_ACTION_LABEL).assertExists()
            composeRule.onNodeWithText(REFRESH_ACTION_LABEL).performClick()
            composeRule.waitForIdle()

            assertTrue(
                "clicking Refresh did not reload the list ViewModel",
                emissions.count { it is SessionListUiState.Loading } > loadsBefore,
            )
        } finally {
            collector.cancel()
        }
    }

    private class NeverCalledRefresher : SessionRefresher {
        override suspend fun refresh(refreshToken: String): RefreshOutcome =
            throw AssertionError("signing out does not refresh the session")
    }

    @Composable
    private fun SessionGate(session: SessionManager, signOut: FakeSignOut) {
        val state by session.state.collectAsStateWithLifecycle()
        when (decideLaunchDestination(hasServerConfigured = true, hasSession = state is SessionState.SignedIn)) {
            LaunchDestination.Home -> AppNavHost(
                onSignOut = { signOut.blockingSignOut() },
                navController = rememberNavController(),
            )
            LaunchDestination.AuthGate -> Text(text = SIGN_IN_SCREEN)
        }
    }

    @Test
    fun `signing out ends the session and returns to the sign-in screen`() {
        val session = SessionManager(
            tokenStore = InMemoryTokenStore(),
            refresher = NeverCalledRefresher(),
            publishAccessToken = {},
        ).apply { establish("access-1", "refresh-1", 900) }
        val signOut = FakeSignOut(session)

        composeRule.setContent { PanelTheme { SessionGate(session, signOut) } }
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertExists()
        composeRule.onNodeWithText(SIGN_IN_SCREEN).assertDoesNotExist()

        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithText(SIGN_OUT_LABEL).performClick()
        composeRule.waitForIdle()

        assertTrue("the session is still alive", session.state.value is SessionState.SignedOut)
        assertNull("the token was not discarded", session.currentAccessToken())
        composeRule.onNodeWithText(SIGN_IN_SCREEN).assertExists()
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertDoesNotExist()
    }

    private class FakeSignOut(private val session: SessionManager) : SignOutSource {
        override suspend fun signOut() = blockingSignOut()
        fun blockingSignOut() {
            session.signOut()
        }
    }

    private companion object {
        const val SIGN_IN_SCREEN = "Sign in to Server Control Panel"

        val JVM_RENDERABLE_DESTINATIONS = listOf(
            AppDestination.Home,
            AppDestination.System,
            AppDestination.Docker,
            AppDestination.Dev,
            AppDestination.Security,
            AppDestination.Operations,
            AppDestination.Settings,
        )
    }
}
