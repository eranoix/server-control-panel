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
    private var contador = 0

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
    }

    private fun novoArquivo() =
        context.getSharedPreferences("teste-aparencia-${contador++}", Context.MODE_PRIVATE)

    @Test
    fun `sem escolha previa o padrao e seguir o sistema`() {
        val pref = ThemePreference(novoArquivo())

        assertEquals(ThemeMode.SISTEMA, pref.current())
        assertEquals(ThemeMode.SISTEMA, pref.mode.value)
    }

    @Test
    fun `a escolha sobrevive a uma nova execucao do app`() {
        val arquivo = novoArquivo()

        // Run 1: the owner chooses light.
        ThemePreference(arquivo).set(ThemeMode.CLARO)

        // Run 2: a NEW instance reading the same disk — this is what a process
        // relaunch does.
        val depoisDeReabrir = ThemePreference(arquivo)
        assertEquals(ThemeMode.CLARO, depoisDeReabrir.current())
    }

    @Test
    fun `voltar para seguir o sistema tambem persiste`() {
        val arquivo = novoArquivo()
        ThemePreference(arquivo).set(ThemeMode.ESCURO)
        ThemePreference(arquivo).set(ThemeMode.SISTEMA)

        assertEquals(ThemeMode.SISTEMA, ThemePreference(arquivo).current())
    }

    @Test
    fun `o valor inicial do fluxo ja e o do disco, sem quadro com o tema errado`() = runTest {
        val arquivo = novoArquivo()
        ThemePreference(arquivo).set(ThemeMode.ESCURO)

        // A StateFlow's `.value` is synchronous: if the read were asynchronous,
        // this value would be the default and the UI would compose light before
        // turning dark. That is exactly the "flash" this test guards against.
        assertEquals(ThemeMode.ESCURO, ThemePreference(arquivo).mode.value)
    }

    @Test
    fun `um valor gravado desconhecido cai no padrao em vez de estourar`() {
        val arquivo = novoArquivo()
        arquivo.edit().putString("modo_tema", "sepia-de-uma-versao-futura").commit()

        assertEquals(ThemeMode.SISTEMA, ThemePreference(arquivo).current())
    }

    @Test
    fun `trocar a escolha publica no fluxo imediatamente`() {
        val pref = ThemePreference(novoArquivo())
        val visto = mutableListOf(pref.mode.value)

        pref.set(ThemeMode.CLARO)
        visto += pref.mode.value
        pref.set(ThemeMode.ESCURO)
        visto += pref.mode.value

        assertEquals(listOf(ThemeMode.SISTEMA, ThemeMode.CLARO, ThemeMode.ESCURO), visto)
    }

    @Test
    fun `get devolve a mesma instancia para o processo inteiro`() {
        // Two screens (MainActivity and ShareTargetActivity) need to see the
        // same choice; two instances would give two truths.
        assertTrue(ThemePreference.get(context) === ThemePreference.get(context))
        // And the direct constructor still gives an isolated instance, which is
        // what the tests above rely on.
        assertNotSame(ThemePreference.get(context), ThemePreference(novoArquivo()))
    }

    @Test
    fun `escolha manual ignora o sistema e seguir o sistema obedece`() {
        // The core of the rule, with no UI: CLARO/ESCURO answer the same with
        // the system in any state; SISTEMA mirrors the state.
        assertEquals(false, ThemeMode.CLARO.escuro(sistemaEscuro = true))
        assertEquals(false, ThemeMode.CLARO.escuro(sistemaEscuro = false))
        assertEquals(true, ThemeMode.ESCURO.escuro(sistemaEscuro = true))
        assertEquals(true, ThemeMode.ESCURO.escuro(sistemaEscuro = false))
        assertEquals(true, ThemeMode.SISTEMA.escuro(sistemaEscuro = true))
        assertEquals(false, ThemeMode.SISTEMA.escuro(sistemaEscuro = false))
    }

    @Test
    fun `os ids gravados sao estaveis`() {
        // Compatibility guard: changing one of these literals turns the
        // already-saved preference of anyone who updates into "system" silently.
        assertEquals("claro", ThemeMode.CLARO.id)
        assertEquals("escuro", ThemeMode.ESCURO.id)
        assertEquals("sistema", ThemeMode.SISTEMA.id)
    }
}
