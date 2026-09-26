package com.vpsmanager.app.nav

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import com.vpsmanager.designsystem.VpsManagerTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The navigation drawer, actually rendered.
 *
 * The defect these tests exist to keep from coming back was seen in a
 * screenshot, not deduced: labels broken over two lines and clipped
 * ("Termin/al", "Notific/ações"), and later "Sign out" pushed off the
 * screen by a new destination. No test caught it because no test drew the
 * drawer.
 *
 * It runs on a real phone geometry (`qualifiers`), and not on
 * Robolectric's tiny default: the height is exactly the variable that
 * decides whether the footer fits.
 */
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
            VpsManagerTheme {
                AppDrawerSheet(
                    currentRoute = currentRoute,
                    onDestinationSelected = onDestinationSelected,
                    onSignOut = onSignOut,
                )
            }
        }
        composeRule.waitForIdle()
    }

    /**
     * The layout actually computed for a text — the only source that knows
     * whether it wrapped or was clipped. An `assertExists` assertion does not
     * know: a label split over two lines still "exists".
     */
    private fun SemanticsNodeInteraction.textLayout(): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        val action = fetchSemanticsNode().config[SemanticsActions.GetTextLayoutResult]
        requireNotNull(action.action) { "nó sem GetTextLayoutResult — não é um texto?" }.invoke(results)
        return results.first()
    }

    @Test
    fun `todo destino aparece com o rotulo inteiro, numa linha so e sem corte`() {
        renderDrawer()

        AppDestination.entries.forEach { destination ->
            val label = composeRule.onNodeWithText(destination.label)
            label.assertExists()
            val layout = label.textLayout()
            assertEquals(
                "rótulo '${destination.label}' quebrou em ${layout.lineCount} linhas",
                1,
                layout.lineCount,
            )
            assertFalse(
                "rótulo '${destination.label}' foi cortado — encurte o nome em vez de deixar cortar",
                layout.hasVisualOverflow,
            )
        }
    }

    @Test
    fun `o rotulo de sair tambem cabe numa linha`() {
        renderDrawer()

        val layout = composeRule.onNodeWithText(SIGN_OUT_LABEL).textLayout()
        assertEquals(1, layout.lineCount)
        assertFalse(layout.hasVisualOverflow)
    }

    @Test
    fun `todo icone tem contentDescription, e nenhuma descricao se repete`() {
        renderDrawer()

        val descriptions = AppDestination.entries.map { it.iconDescription } + SIGN_OUT_ICON_DESCRIPTION
        descriptions.forEach { description ->
            composeRule.onNodeWithContentDescription(description).assertExists()
        }
        // Repeated descriptions would make the screen reader announce two
        // different items with the same name.
        assertEquals(descriptions.size, descriptions.toSet().size)
    }

    /**
     * The drawer lists the web panel's PARENT pages, and only those.
     *
     * It is the fix for the defect the owner pointed out: there were two
     * taxonomies for the same product (the drawer with loose screens,
     * Administration with thirty blocks), and Jira appeared in both.
     */
    @Test
    fun `a gaveta tem exatamente as maes do painel, na ordem da web`() {
        renderDrawer()

        val expected = listOf(
            "Home", "System", "Docker", "Dev", "Security", "Apps", "Operations", "Settings",
        )
        assertEquals(expected, AppDestination.entries.map { it.label })
        expected.forEach { composeRule.onNodeWithText(it).assertExists() }
    }

    @Test
    fun `inicio e o primeiro destino da gaveta`() {
        renderDrawer()

        val homeTop = composeRule.onNodeWithText(AppDestination.Home.label)
            .fetchSemanticsNode().boundsInRoot.top
        AppDestination.entries.filter { it != AppDestination.Home }.forEach { other ->
            val top = composeRule.onNodeWithText(other.label).fetchSemanticsNode().boundsInRoot.top
            assertTrue("'${other.label}' aparece acima do Início", top > homeTop)
        }
    }

    @Test
    fun `configuracoes fica por ultimo, depois de todo destino de trabalho`() {
        renderDrawer()

        val configTop = composeRule.onNodeWithText(AppDestination.Settings.label)
            .fetchSemanticsNode().boundsInRoot.top
        AppDestination.entries.filter { it != AppDestination.Settings }.forEach { other ->
            val top = composeRule.onNodeWithText(other.label).fetchSemanticsNode().boundsInRoot.top
            assertTrue("'${other.label}' ficou abaixo de Configurações", top < configTop)
        }
    }

    @Test
    fun `escolher um destino avisa quem navega`() {
        var selected: AppDestination? = null
        renderDrawer(onDestinationSelected = { selected = it })

        composeRule.onNodeWithText(AppDestination.Apps.label).performClick()

        assertEquals(AppDestination.Apps, selected)
    }

    /**
     * The footer stays ANCHORED: signing out must not depend on discovering
     * that the drawer scrolls.
     *
     * That is how "Sign out" vanished when a new destination arrived — the
     * whole drawer scrolled, and each addition pushed the footer further out.
     */
    @Test
    fun `sair fica visivel sem rolar, e e clicavel`() {
        var signedOut = false
        renderDrawer(onSignOut = { signedOut = true })

        val signOut = composeRule.onNodeWithText(SIGN_OUT_LABEL)
        signOut.assertIsDisplayed()
        signOut.assertHasClickAction()

        val signOutTop = signOut.fetchSemanticsNode().boundsInRoot.top
        AppDestination.entries.forEach { destination ->
            val top = composeRule.onNodeWithText(destination.label).fetchSemanticsNode().boundsInRoot.top
            assertTrue("'${destination.label}' ficou abaixo de Sair", top < signOutTop)
        }

        signOut.performClick()
        assertTrue(signedOut)
    }

    /**
     * Appearance and the update check LEFT the drawer.
     *
     * A device setting is not a work destination; mixing it with System,
     * Docker and Operations made the drawer answer two different questions.
     * They now live in Settings.
     */
    @Test
    fun `aparencia e procurar atualizacoes nao estao mais na gaveta`() {
        renderDrawer()

        composeRule.onNodeWithText(CHECK_UPDATE_LABEL).assertDoesNotExist()
        composeRule.onNodeWithText("Appearance").assertDoesNotExist()
    }

    @Test
    fun `o destino atual aparece marcado, e so ele`() {
        assertEquals(
            listOf(AppDestination.Docker),
            AppDestination.entries.filter { it.matches(AppDestination.Docker.route) },
        )
    }

    /**
     * A parent matches its own grid, never the child screens.
     *
     * Highlighting "Docker" while the header says "Containers" would have the
     * drawer claiming two different things at the same time.
     */
    @Test
    fun `mae nao casa com a rota de uma filha`() {
        assertTrue(AppDestination.Docker.matches(parentRoute("docker")))
        assertFalse(AppDestination.Docker.matches(adminSectionRoute("docker.containers")))
        assertFalse(AppDestination.Operations.matches(ROUTE_JIRA))
    }

    /** A duplicate route would leave two items highlighted at the same time. */
    @Test
    fun `nenhuma rota se repete entre destinos`() {
        val routes = AppDestination.entries.map { it.route }
        assertEquals(routes.size, routes.toSet().size)
    }
}
