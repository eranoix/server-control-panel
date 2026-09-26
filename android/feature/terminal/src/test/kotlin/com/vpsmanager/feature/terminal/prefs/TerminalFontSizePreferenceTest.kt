package com.vpsmanager.feature.terminal.prefs

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.File

/**
 * Each test builds its OWN [androidx.datastore.core.DataStore] over a fresh
 * file in [tempFolder] rather than the app-wide `by preferencesDataStore(...)`
 * singleton [TerminalFontSizePreference] defaults to in production: that
 * singleton is cached per [android.content.Context] instance for the process
 * lifetime, and Robolectric can hand back the same application context
 * across `@Test` methods, so a shared store would leak a value one test
 * wrote into whichever test happens to run next.
 */
@RunWith(RobolectricTestRunner::class)
class TerminalFontSizePreferenceTest {

    @get:Rule
    val tempFolder = TemporaryFolder()

    private fun newPreference(): TerminalFontSizePreference {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val store = PreferenceDataStoreFactory.create(
            produceFile = { File(tempFolder.newFolder(), "test.preferences_pb") },
        )
        return TerminalFontSizePreference(context, store)
    }

    @Test
    fun `default font size is emitted before any value was ever set`() = runTest {
        val pref = newPreference()
        assertEquals(TerminalFontSizePreference.DEFAULT_FONT_SIZE_SP, pref.fontSizeSp.first())
    }

    @Test
    fun `a set value is persisted and re-emitted`() = runTest {
        val pref = newPreference()
        pref.setFontSizeSp(20f)
        assertEquals(20f, pref.fontSizeSp.first())
    }

    @Test
    fun `a value below the minimum is clamped up`() = runTest {
        val pref = newPreference()
        pref.setFontSizeSp(2f)
        assertEquals(TerminalFontSizePreference.MIN_FONT_SIZE_SP, pref.fontSizeSp.first())
    }

    @Test
    fun `a value above the maximum is clamped down`() = runTest {
        val pref = newPreference()
        pref.setFontSizeSp(999f)
        assertEquals(TerminalFontSizePreference.MAX_FONT_SIZE_SP, pref.fontSizeSp.first())
    }

    @Test
    fun `a new instance over the same backing file reads back the previously persisted value`() = runTest {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val file = File(tempFolder.newFolder(), "shared.preferences_pb")
        val store: androidx.datastore.core.DataStore<Preferences> =
            PreferenceDataStoreFactory.create(produceFile = { file })

        TerminalFontSizePreference(context, store).setFontSizeSp(24f)
        val reread = TerminalFontSizePreference(context, store)

        assertEquals(24f, reread.fontSizeSp.first())
    }
}
