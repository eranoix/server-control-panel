package com.vpsmanager.app.nav

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import com.vpsmanager.designsystem.THEME_SELECTOR_LABEL
import com.vpsmanager.designsystem.ThemeMode
import com.vpsmanager.designsystem.VpsManagerTheme
import com.vpsmanager.designsystem.themeOptionTag
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The appearance selector (now in Settings): reachable, highlighted and with no clipped label.
 * Uses the same phone geometry as [AppDrawerTest], since three labels side by side are what
 * gets clipped on a real device.
 */
@RunWith(RobolectricTestRunner::class)
@Config(application = android.app.Application::class, qualifiers = "w411dp-h891dp-xxhdpi")
class AppDrawerThemeTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun renderDrawer(
        themeMode: ThemeMode = ThemeMode.DEFAULT,
        onThemeModeChange: (ThemeMode) -> Unit = {},
    ) {
        composeRule.setContent {
            VpsManagerTheme(themeMode = themeMode) {
                SettingsScreen(
                    themeMode = themeMode,
                    onThemeModeChange = onThemeModeChange,
                    installedVersion = "0.1.42",
                    onCheckForUpdate = {},
                    onOpenNotifications = {},
                    onOpenSecurity = {},
                    onOpenLicenses = {},
                    onOpenDiagnostics = {},
                onOpenStorage = {},
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
    fun `settings offers the three appearances without navigating anywhere`() {
        renderDrawer()

        composeRule.onNodeWithText(THEME_SELECTOR_LABEL).assertIsDisplayed()
        ThemeMode.entries.forEach { mode ->
            composeRule.onNodeWithTag(themeOptionTag(mode)).assertIsDisplayed()
        }
    }

    @Test
    fun `the current mode is shown selected`() {
        renderDrawer(themeMode = ThemeMode.DARK)

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.DARK)).assertIsSelected()
    }

    @Test
    fun `tapping an appearance reports the choice`() {
        var chosen: ThemeMode? = null
        renderDrawer(themeMode = ThemeMode.SYSTEM, onThemeModeChange = { chosen = it })

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.LIGHT)).performClick()

        assertEquals(ThemeMode.LIGHT, chosen)
    }

    @Test
    fun `no selector label wraps onto two lines on the real screen`() {
        renderDrawer()

        // ThemeModeSelectorWidthTest checks the component alone; this checks it with the
        // real screen's padding and neighbours.
        ThemeMode.entries.forEach { mode ->
            val layout = composeRule.onNodeWithText(mode.label).textLayout()
            assertEquals("label \"${mode.label}\" wrapped onto more than one line", 1, layout.lineCount)
        }
    }

    @Test
    fun `the appearance selector opens Settings, above the other settings`() {
        renderDrawer()

        // Appearance comes first: it is changed most often and its effect is immediate.
        val selector = composeRule.onNodeWithText(THEME_SELECTOR_LABEL)
            .fetchSemanticsNode().positionInRoot.y
        val update = composeRule.onNodeWithText(CHECK_UPDATE_LABEL)
            .fetchSemanticsNode().positionInRoot.y

        assertTrue("the appearance selector fell below the other settings", selector < update)
    }
}
