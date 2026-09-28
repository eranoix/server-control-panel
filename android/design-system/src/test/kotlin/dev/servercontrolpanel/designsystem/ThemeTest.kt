package dev.servercontrolpanel.designsystem

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

@RunWith(RobolectricTestRunner::class)
class ThemeTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun systemDark(dark: Boolean) {
        RuntimeEnvironment.setQualifiers(if (dark) "+night" else "+notnight")
    }

    private fun appliedSurface(themeMode: ThemeMode): Float {
        var surface = Color.Unspecified
        composeRule.setContent {
            PanelTheme(themeMode = themeMode) {
                surface = MaterialTheme.colorScheme.surface
                Text("x")
            }
        }
        composeRule.waitForIdle()
        return surface.luminance()
    }

    @Test
    fun `SYSTEM renders dark when the system is dark`() {
        systemDark(true)
        assertTrue(appliedSurface(ThemeMode.SYSTEM) < 0.5f)
    }

    @Test
    fun `SYSTEM renders light when the system is light`() {
        systemDark(false)
        assertTrue(appliedSurface(ThemeMode.SYSTEM) >= 0.5f)
    }

    @Test
    fun `LIGHT ignores a dark system`() {
        systemDark(true)
        assertTrue(appliedSurface(ThemeMode.LIGHT) >= 0.5f)
    }

    @Test
    fun `DARK ignores a light system`() {
        systemDark(false)
        assertTrue(appliedSurface(ThemeMode.DARK) < 0.5f)
    }

    @Test
    fun `status colors follow the manual choice, not the system`() {
        systemDark(true)
        var notification = StatusColorPair(Color.Unspecified, Color.Unspecified, Color.Unspecified)
        composeRule.setContent {
            PanelTheme(themeMode = ThemeMode.LIGHT) {
                notification = panelStatusColors.warning
                Text("x")
            }
        }
        composeRule.waitForIdle()

        assertTrue("warning container should be light", notification.container.luminance() > 0.5f)
        assertTrue("warning text should be dark", notification.content.luminance() < 0.5f)
    }

    @Test
    fun `changing the choice repaints without recreating the screen`() {
        systemDark(false)
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var surface = Color.Unspecified
        composeRule.setContent {
            PanelTheme(themeMode = mode) {
                surface = MaterialTheme.colorScheme.surface
                Text("x")
            }
        }
        composeRule.waitForIdle()
        assertTrue(surface.luminance() >= 0.5f)

        mode = ThemeMode.DARK
        composeRule.waitForIdle()
        assertTrue("the change must apply on recomposition", surface.luminance() < 0.5f)
    }

    @Test
    fun `the selector marks the current mode and reports the chosen one`() {
        var chosen: ThemeMode? = null
        composeRule.setContent {
            PanelTheme(themeMode = ThemeMode.SYSTEM) {
                ThemeModeSelector(selected = ThemeMode.SYSTEM, onSelect = { chosen = it })
            }
        }

        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.SYSTEM)).assertIsSelected()
        composeRule.onNodeWithTag(themeOptionTag(ThemeMode.LIGHT)).performClick()

        assertEquals(ThemeMode.LIGHT, chosen)
    }

    @Test
    fun `the selector offers all three options`() {
        composeRule.setContent {
            PanelTheme {
                ThemeModeSelector(selected = ThemeMode.SYSTEM, onSelect = {})
            }
        }

        ThemeMode.entries.forEach { mode ->
            composeRule.onNodeWithTag(themeOptionTag(mode)).assertExists()
        }
    }
}
