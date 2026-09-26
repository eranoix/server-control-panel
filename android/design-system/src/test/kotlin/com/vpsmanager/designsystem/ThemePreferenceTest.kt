package com.vpsmanager.designsystem

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
 * The appearance preference's contract: what was chosen STAYS chosen on the
 * next run, and "follow the system" stays the default for whoever never chose.
 *
 * Each test uses its own preferences file: Robolectric's `SharedPreferences`
 * is cached by name within the process, so two tests with the same name would
 * leak values into each other and the persistence test would pass even with
 * the write broken.
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
        context.getSharedPreferences("teste-aparencia-${counter++}", Context.MODE_PRIVATE)

    @Test
    fun `sem escolha previa o padrao e seguir o sistema`() {
        val pref = ThemePreference(newFile())

        assertEquals(ThemeMode.SYSTEM, pref.current())
        assertEquals(ThemeMode.SYSTEM, pref.mode.value)
    }

    @Test
    fun `a escolha sobrevive a uma nova execucao do app`() {
        val file = newFile()

        // Run 1: the owner chooses light.
        ThemePreference(file).set(ThemeMode.LIGHT)

        // Run 2: a NEW instance reading the same disk — this is what a process
        // relaunch does.
        val afterReopen = ThemePreference(file)
        assertEquals(ThemeMode.LIGHT, afterReopen.current())
    }

    @Test
    fun `voltar para seguir o sistema tambem persiste`() {
        val file = newFile()
        ThemePreference(file).set(ThemeMode.DARK)
        ThemePreference(file).set(ThemeMode.SYSTEM)

        assertEquals(ThemeMode.SYSTEM, ThemePreference(file).current())
    }

    @Test
    fun `o valor inicial do fluxo ja e o do disco, sem quadro com o tema errado`() = runTest {
        val file = newFile()
        ThemePreference(file).set(ThemeMode.DARK)

        // A StateFlow's `.value` is synchronous: if the read were asynchronous,
        // this value would be the default and the UI would compose light before
        // turning dark. That is exactly the "flash" this test guards against.
        assertEquals(ThemeMode.DARK, ThemePreference(file).mode.value)
    }

    @Test
    fun `um valor gravado desconhecido cai no padrao em vez de estourar`() {
        val file = newFile()
        file.edit().putString("modo_tema", "sepia-de-uma-versao-futura").commit()

        assertEquals(ThemeMode.SYSTEM, ThemePreference(file).current())
    }

    @Test
    fun `trocar a escolha publica no fluxo imediatamente`() {
        val pref = ThemePreference(newFile())
        val seen = mutableListOf(pref.mode.value)

        pref.set(ThemeMode.LIGHT)
        seen += pref.mode.value
        pref.set(ThemeMode.DARK)
        seen += pref.mode.value

        assertEquals(listOf(ThemeMode.SYSTEM, ThemeMode.LIGHT, ThemeMode.DARK), seen)
    }

    @Test
    fun `get devolve a mesma instancia para o processo inteiro`() {
        // Two screens (MainActivity and ShareTargetActivity) need to see the
        // same choice; two instances would give two truths.
        assertTrue(ThemePreference.get(context) === ThemePreference.get(context))
        // And the direct constructor still gives an isolated instance, which is
        // what the tests above rely on.
        assertNotSame(ThemePreference.get(context), ThemePreference(newFile()))
    }

    @Test
    fun `escolha manual ignora o sistema e seguir o sistema obedece`() {
        // The core of the rule, with no UI: CLARO/ESCURO answer the same with
        // the system in any state; SISTEMA mirrors the state.
        assertEquals(false, ThemeMode.LIGHT.dark(systemDark = true))
        assertEquals(false, ThemeMode.LIGHT.dark(systemDark = false))
        assertEquals(true, ThemeMode.DARK.dark(systemDark = true))
        assertEquals(true, ThemeMode.DARK.dark(systemDark = false))
        assertEquals(true, ThemeMode.SYSTEM.dark(systemDark = true))
        assertEquals(false, ThemeMode.SYSTEM.dark(systemDark = false))
    }

    @Test
    fun `os ids gravados sao estaveis`() {
        // Compatibility guard: changing one of these literals turns the
        // already-saved preference of anyone who updates into "system" silently.
        assertEquals("claro", ThemeMode.LIGHT.id)
        assertEquals("escuro", ThemeMode.DARK.id)
        assertEquals("sistema", ThemeMode.SYSTEM.id)
    }
}
