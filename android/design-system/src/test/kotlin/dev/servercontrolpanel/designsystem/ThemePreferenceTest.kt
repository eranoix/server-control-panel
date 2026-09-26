package dev.servercontrolpanel.designsystem

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The appearance choice persists across runs, and following the system is the default.
 *
 * Each test uses its own preferences file because Robolectric caches
 * `SharedPreferences` by name, which would leak values between tests.
 */
@RunWith(RobolectricTestRunner::class)
class ThemePreferenceTest {

    private lateinit var context: Context
    private var counter = 0

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
    }

    private fun newFile() =
        context.getSharedPreferences("test-appearance-${counter++}", Context.MODE_PRIVATE)

    @Test
    fun `with no prior choice the default is to follow the system`() {
        val pref = ThemePreference(newFile())

        assertEquals(ThemeMode.SYSTEM, pref.current())
        assertEquals(ThemeMode.SYSTEM, pref.mode.value)
    }

    @Test
    fun `the choice survives an app restart`() {
        val file = newFile()

        ThemePreference(file).set(ThemeMode.LIGHT)

        // A new instance on the same file simulates a process restart.
        val afterReopen = ThemePreference(file)
        assertEquals(ThemeMode.LIGHT, afterReopen.current())
    }

    @Test
    fun `switching back to system also persists`() {
        val file = newFile()
        ThemePreference(file).set(ThemeMode.DARK)
        ThemePreference(file).set(ThemeMode.SYSTEM)

        assertEquals(ThemeMode.SYSTEM, ThemePreference(file).current())
    }

    @Test
    fun `the flow's initial value is already the stored one, so no wrong-theme frame`() = runTest {
        val file = newFile()
        ThemePreference(file).set(ThemeMode.DARK)

        // An async read would expose the default first and flash the wrong theme.
        assertEquals(ThemeMode.DARK, ThemePreference(file).mode.value)
    }

    @Test
    fun `an unknown stored value falls back to the default instead of crashing`() {
        val file = newFile()
        file.edit().putString("theme_mode", "sepia-from-a-future-version").commit()

        assertEquals(ThemeMode.SYSTEM, ThemePreference(file).current())
    }

    @Test
    fun `changing the choice publishes it on the flow immediately`() {
        val pref = ThemePreference(newFile())
        val seen = mutableListOf(pref.mode.value)

        pref.set(ThemeMode.LIGHT)
        seen += pref.mode.value
        pref.set(ThemeMode.DARK)
        seen += pref.mode.value

        assertEquals(listOf(ThemeMode.SYSTEM, ThemeMode.LIGHT, ThemeMode.DARK), seen)
    }

    @Test
    fun `get returns the same instance for the whole process`() {
        assertTrue(ThemePreference.get(context) === ThemePreference.get(context))
        // The constructor still gives an isolated instance, which the tests above rely on.
        assertNotSame(ThemePreference.get(context), ThemePreference(newFile()))
    }

    @Test
    fun `a manual choice ignores the system and SYSTEM follows it`() {
        assertEquals(false, ThemeMode.LIGHT.dark(systemDark = true))
        assertEquals(false, ThemeMode.LIGHT.dark(systemDark = false))
        assertEquals(true, ThemeMode.DARK.dark(systemDark = true))
        assertEquals(true, ThemeMode.DARK.dark(systemDark = false))
        assertEquals(true, ThemeMode.SYSTEM.dark(systemDark = true))
        assertEquals(false, ThemeMode.SYSTEM.dark(systemDark = false))
    }

    @Test
    fun `stored ids are stable`() {
        // Changing an id would silently reset existing users' saved choice to SYSTEM.
        assertEquals("light", ThemeMode.LIGHT.id)
        assertEquals("dark", ThemeMode.DARK.id)
        assertEquals("system", ThemeMode.SYSTEM.id)
    }
}
