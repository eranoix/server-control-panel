package dev.servercontrolpanel.app.nav

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import dev.servercontrolpanel.designsystem.PanelTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class AppDrawerTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun renderDrawer(
        currentRoute: String? = AppDestination.Home.route,
        onDestinationSelected: (AppDestination) -> Unit = {},
        onSignOut: () -> Unit = {},
    ) {
        composeRule.setContent {
            PanelTheme {
                AppDrawerSheet(
                    currentRoute = currentRoute,
                    onDestinationSelected = onDestinationSelected,
                    onSignOut = onSignOut,
                )
            }
        }
        composeRule.waitForIdle()
    }

    private fun SemanticsNodeInteraction.textLayout(): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        val action = fetchSemanticsNode().config[SemanticsActions.GetTextLayoutResult]
        requireNotNull(action.action) { "node without GetTextLayoutResult, is it not a text?" }.invoke(results)
        return results.first()
    }

    @Test
    fun `every destination shows its full label on one line without clipping`() {
        renderDrawer()

        AppDestination.entries.forEach { destination ->
            val label = composeRule.onNodeWithText(destination.label)
            label.assertExists()
            val layout = label.textLayout()
            assertEquals(
                "label '${destination.label}' wrapped onto ${layout.lineCount} lines",
                1,
                layout.lineCount,
            )
            assertFalse(
                "label '${destination.label}' was clipped, shorten the name instead",
                layout.hasVisualOverflow,
            )
        }
    }

    @Test
    fun `the sign out label also fits on one line`() {
        renderDrawer()

        val layout = composeRule.onNodeWithText(SIGN_OUT_LABEL).textLayout()
        assertEquals(1, layout.lineCount)
        assertFalse(layout.hasVisualOverflow)
    }

    @Test
    fun `every icon has a contentDescription and none repeats`() {
        renderDrawer()

        val descriptions = AppDestination.entries.map { it.iconDescription } + SIGN_OUT_ICON_DESCRIPTION
        descriptions.forEach { description ->
            composeRule.onNodeWithContentDescription(description).assertExists()
        }
        assertEquals(descriptions.size, descriptions.toSet().size)
    }

    @Test
    fun `the drawer has exactly the panel's parents in web order`() {
        renderDrawer()

        val expected = listOf(
            "Home", "System", "Docker", "Dev", "Security", "Apps", "Operations", "Settings",
        )
        assertEquals(expected, AppDestination.entries.map { it.label })
        expected.forEach { composeRule.onNodeWithText(it).assertExists() }
    }

    @Test
    fun `home is the first drawer destination`() {
        renderDrawer()

        val homeTop = composeRule.onNodeWithText(AppDestination.Home.label)
            .fetchSemanticsNode().boundsInRoot.top
        AppDestination.entries.filter { it != AppDestination.Home }.forEach { other ->
            val top = composeRule.onNodeWithText(other.label).fetchSemanticsNode().boundsInRoot.top
            assertTrue("'${other.label}' appears above Home", top > homeTop)
        }
    }

    @Test
    fun `settings comes last, after every work destination`() {
        renderDrawer()

        val configTop = composeRule.onNodeWithText(AppDestination.Settings.label)
            .fetchSemanticsNode().boundsInRoot.top
        AppDestination.entries.filter { it != AppDestination.Settings }.forEach { other ->
            val top = composeRule.onNodeWithText(other.label).fetchSemanticsNode().boundsInRoot.top
            assertTrue("'${other.label}' ended up below Settings", top < configTop)
        }
    }

    @Test
    fun `picking a destination notifies the navigator`() {
        var selected: AppDestination? = null
        renderDrawer(onDestinationSelected = { selected = it })

        composeRule.onNodeWithText(AppDestination.Apps.label).performClick()

        assertEquals(AppDestination.Apps, selected)
    }

    @Test
    fun `sign out is visible without scrolling and is clickable`() {
        var signedOut = false
        renderDrawer(onSignOut = { signedOut = true })

        val signOut = composeRule.onNodeWithText(SIGN_OUT_LABEL)
        signOut.assertIsDisplayed()
        signOut.assertHasClickAction()

        val signOutTop = signOut.fetchSemanticsNode().boundsInRoot.top
        AppDestination.entries.forEach { destination ->
            val top = composeRule.onNodeWithText(destination.label).fetchSemanticsNode().boundsInRoot.top
            assertTrue("'${destination.label}' ended up below Sign out", top < signOutTop)
        }

        signOut.performClick()
        assertTrue(signedOut)
    }

    @Test
    fun `appearance and check for updates are not in the drawer`() {
        renderDrawer()

        composeRule.onNodeWithText(CHECK_UPDATE_LABEL).assertDoesNotExist()
        composeRule.onNodeWithText("Appearance").assertDoesNotExist()
    }

    @Test
    fun `only the current destination is highlighted`() {
        assertEquals(
            listOf(AppDestination.Docker),
            AppDestination.entries.filter { it.matches(AppDestination.Docker.route) },
        )
    }

    @Test
    fun `a parent does not match a child's route`() {
        assertTrue(AppDestination.Docker.matches(parentRoute("docker")))
        assertFalse(AppDestination.Docker.matches(adminSectionRoute("docker.containers")))
        assertFalse(AppDestination.Operations.matches(ROUTE_JIRA))
    }

    @Test
    fun `no route repeats across destinations`() {
        val routes = AppDestination.entries.map { it.route }
        assertEquals(routes.size, routes.toSet().size)
    }
}
