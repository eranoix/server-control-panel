package dev.servercontrolpanel.app.nav

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.navigation.NavHostController
import androidx.navigation.compose.rememberNavController
import dev.servercontrolpanel.data.update.UpdateRecovery
import dev.servercontrolpanel.data.update.UpdateState
import dev.servercontrolpanel.designsystem.PanelTheme
import java.net.URLEncoder
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The update banner's placement inside the shell (its design is covered by `UpdateBannerTest`):
 * in the `Scaffold`'s `topBar` slot on drawer destinations, and never on detail screens.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class UpdateBannerInShellTest {

    @get:Rule
    val composeRule = createComposeRule()

    private lateinit var navController: NavHostController

    private fun renderShell(
        updateState: UpdateState,
        onUpdateClick: () -> Unit = {},
        onUpdateRecovery: (UpdateRecovery) -> Unit = {},
        updateDiagnostics: String? = null,
    ) {
        composeRule.setContent {
            PanelTheme {
                navController = rememberNavController()
                AppNavHost(
                    updateState = updateState,
                    onUpdateClick = onUpdateClick,
                    onUpdateRecovery = onUpdateRecovery,
                    updateDiagnostics = updateDiagnostics,
                    navController = navController,
                )
            }
        }
        composeRule.waitForIdle()
    }

    @Test
    fun `the banner shows in the shell next to the hamburger without hiding content`() {
        renderShell(UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true))

        composeRule.onNodeWithText("Version 0.1.7 available — 1.4 MB").assertIsDisplayed()
        // The banner is added below the bar, not in its place.
        composeRule.onNodeWithContentDescription(OPEN_DRAWER_DESCRIPTION).assertIsDisplayed()
    }

    @Test
    fun `without an update no banner takes up height`() {
        renderShell(UpdateState.Idle)

        composeRule.onNodeWithText("Update", substring = true).assertDoesNotExist()
    }

    /** The banner uses the same condition as the shell bar, so it never shows on detail screens. */
    @Test
    fun `the banner disappears on detail screens`() {
        renderShell(UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true))
        composeRule.onNodeWithText("Version 0.1.7 available — 1.4 MB").assertIsDisplayed()

        navController.navigate("files/edit/" + URLEncoder.encode("/etc/hosts", "UTF-8"))
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Version 0.1.7 available — 1.4 MB").assertDoesNotExist()
    }

    @Test
    fun `tapping the banner requests the update`() {
        var requests = 0
        renderShell(
            UpdateState.Available(versionName = "0.1.7", downloadBytes = 1_400_329, incremental = true),
            onUpdateClick = { requests++ },
        )

        composeRule.onNodeWithText("Update").performClick()

        assertEquals(1, requests)
    }

    /**
     * A failed install's `PackageInstaller` message does not fit in a banner, so "Diagnostics"
     * navigates inside the app instead of going up to `MainActivity` as an [UpdateRecovery].
     */
    @Test
    fun `the Diagnostics button navigates to the report with the system message`() {
        var exitsToSystem = 0
        renderShell(
            UpdateState.Failed(
                "Installation failed: INSTALL_FAILED_UPDATE_INCOMPATIBLE",
                canRetry = true,
                recovery = UpdateRecovery.SHOW_DIAGNOSTICS,
            ),
            onUpdateRecovery = { exitsToSystem++ },
            updateDiagnostics = "Update: installation failed (status 4)\nINSTALL_FAILED_UPDATE_INCOMPATIBLE",
        )

        composeRule.onNodeWithText("Diagnostics").performClick()
        composeRule.waitForIdle()

        assertEquals("diagnostics", navController.currentBackStackEntry?.destination?.route)
        assertEquals("this must not leave the app", 0, exitsToSystem)
        composeRule.onNodeWithText("INSTALL_FAILED_UPDATE_INCOMPATIBLE", substring = true).assertExists()
    }

    /** Recoveries that belong to the system (toggle, storage, browser) go up to the Activity. */
    @Test
    fun `other recoveries go up to the Activity instead of becoming navigation`() {
        var requested: UpdateRecovery? = null
        renderShell(
            UpdateState.Failed("no permission", canRetry = true, recovery = UpdateRecovery.ALLOW_UNKNOWN_SOURCES),
            onUpdateRecovery = { requested = it },
        )

        composeRule.onNodeWithText("Allow").performClick()

        assertEquals(UpdateRecovery.ALLOW_UNKNOWN_SOURCES, requested)
    }
}
