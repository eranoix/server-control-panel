package com.vpsmanager.feature.terminal.selection

import android.view.Menu
import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.vpsmanager.feature.terminal.input.TerminalInputView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The SYSTEM's floating bar over the terminal.
 *
 * What this test proves, and no JVM test could, is that Android really does
 * **grant** a floating-type `ActionMode` to this `View`. A `startActionMode`
 * may return `null` — if the view is not attached, if the window does not
 * support it, if the theme has no action mode — and in that case the bar
 * would simply not appear, silently. Only by running on a device can you
 * know.
 *
 * Before this there were "Copiar"/"Colar" buttons drawn by the app at the
 * foot of the grid. This is the object that replaced them.
 */
@RunWith(AndroidJUnit4::class)
class TerminalActionModeTest {

    @get:Rule
    val composeTestRule = createAndroidComposeRule<ComponentActivity>()

    private fun comBarra(
        bloco: (barra: TerminalActionMode, registro: Registro) -> Unit,
    ) {
        val registro = Registro()
        lateinit var barra: TerminalActionMode
        composeTestRule.runOnUiThread {
            val host = TerminalInputView(composeTestRule.activity)
            // Attach it for real: a loose view never gets an `ActionMode`.
            val raiz = composeTestRule.activity.findViewById<ViewGroup>(android.R.id.content)
            raiz.addView(host)
            host.requestFocus()
            barra = TerminalActionMode(
                host = host,
                aoCopiar = { registro.copiou++ },
                aoSelecionarTudo = { registro.selecionouTudo++ },
                aoColar = { registro.colou++ },
                aoCompartilhar = { registro.compartilhou++ },
                aoEnviarProTerminal = { registro.enviou++ },
                aoFechar = { registro.fechou++ },
                retanguloDaSelecao = { Rect(0f, 0f, 120f, 40f) },
            )
        }
        composeTestRule.waitForIdle()
        bloco(barra, registro)
    }

    private class Registro {
        var copiou = 0
        var selecionouTudo = 0
        var colou = 0
        var compartilhou = 0
        var enviou = 0
        var fechou = 0
    }

    @Test
    fun oSistemaConcedeUmModoDeAcaoFlutuanteParaAgradeDoTerminal() = comBarra { barra, _ ->
        composeTestRule.runOnUiThread { barra.mostrar() }
        composeTestRule.waitForIdle()

        assertTrue(
            "sem um ActionMode concedido pelo sistema não existe barra flutuante nenhuma — e a falha seria silenciosa",
            barra.estaNoAr(),
        )
    }

    @Test
    fun mostrarDuasVezes_naoAbreUmaSegundaBarra() = comBarra { barra, _ ->
        composeTestRule.runOnUiThread {
            barra.mostrar()
            // The selection changed size: re-anchor, do not stack another bar.
            barra.mostrar()
            barra.atualizar()
        }
        composeTestRule.waitForIdle()

        assertTrue(barra.estaNoAr())
    }

    @Test
    fun esconderPorDentro_naoAvisaDeVoltaQuemFechou() = comBarra { barra, registro ->
        // This is the anti-recursion latch: whoever hides the bar is usually
        // whoever cleared the selection, and telling them back would call
        // `esconder()` again, without end.
        composeTestRule.runOnUiThread {
            barra.mostrar()
            barra.esconder()
        }
        composeTestRule.waitForIdle()

        assertFalse(barra.estaNoAr())
        assertEquals("fechar por dentro não pode ecoar de volta", 0, registro.fechou)
    }

    @Test
    fun esconderSemBarraNoAr_naoFazNada() = comBarra { barra, registro ->
        composeTestRule.runOnUiThread { barra.esconder() }
        composeTestRule.waitForIdle()

        assertFalse(barra.estaNoAr())
        assertEquals(0, registro.fechou)
    }

    // ---- The REAL menu the system assembled ----
    //
    // `MenuDeSelecaoTest` proves the hierarchy over the list of items, on the
    // JVM. These prove the next step, which only exists on the device: that
    // the items arrived intact at the `Menu` of the `ActionMode` granted by
    // the system, with the labels Android itself translates, and that the
    // click dispatches through the right action.

    @Test
    fun oMenuDoSistemaRecebeTodasAsAcoes_comOsRotulosDoAparelho() = comBarra { barra, _ ->
        composeTestRule.runOnUiThread { barra.mostrar() }
        composeTestRule.waitForIdle()

        val menu = checkNotNull(barra.menuNoAr()) { "sem menu não há barra" }
        assertEquals(itensDaBarra().size, menu.size())
        for (item in itensDaBarra()) {
            val entrada = checkNotNull(menu.findItem(item.id)) { "faltou ${item.acao} no menu real" }
            val esperado = item.tituloDoSistema
                ?.let { composeTestRule.activity.getString(it) }
                ?: item.tituloProprio
            assertEquals("rótulo de ${item.acao}", esperado, entrada.title.toString())
            assertEquals("ordem de ${item.acao}", item.ordem, entrada.order)
            // The item has to be visible AND enabled: `FloatingToolbar`
            // filters by `isVisible() && isEnabled()`, and a disabled item
            // disappears from the bar instead of going grey.
            assertTrue("${item.acao} tem que estar visível", entrada.isVisible)
            assertTrue("${item.acao} tem que estar habilitada", entrada.isEnabled)
        }
    }

    @Test
    fun colarEstaNoMenuMesmoSemNadaCopiado() = comBarra { barra, _ ->
        // The emulator's clipboard starts out empty in this test, and that is
        // exactly the case that used to make the item disappear. Now it stays —
        // the "there is nothing copied" is said on the click, by `PasteAction`.
        composeTestRule.runOnUiThread { barra.mostrar() }
        composeTestRule.waitForIdle()

        val colar = checkNotNull(barra.menuNoAr()).findItem(Menu.FIRST + AcaoDeSelecao.COLAR.ordinal)
        assertTrue("Colar não pode depender do que há na área de transferência", colar != null)
    }

    @Test
    fun clicarEmCadaItem_despachaAAcaoCorrespondente() = comBarra { barra, registro ->
        composeTestRule.runOnUiThread { barra.mostrar() }
        composeTestRule.waitForIdle()

        // "Selecionar tudo" is the only one that does NOT close the bar, so it
        // goes first: the others end the mode and the menu stops existing.
        acionar(barra, AcaoDeSelecao.SELECIONAR_TUDO)
        assertEquals(1, registro.selecionouTudo)
        assertTrue("selecionar tudo troca a seleção, não encerra o gesto", barra.estaNoAr())

        acionar(barra, AcaoDeSelecao.COPIAR)
        assertEquals(1, registro.copiou)
        assertFalse("copiar encerra a seleção", barra.estaNoAr())

        reabrirEAcionar(barra, AcaoDeSelecao.COLAR)
        assertEquals(1, registro.colou)

        reabrirEAcionar(barra, AcaoDeSelecao.COMPARTILHAR)
        assertEquals(1, registro.compartilhou)

        reabrirEAcionar(barra, AcaoDeSelecao.ENVIAR_PRO_TERMINAL)
        assertEquals(1, registro.enviou)
    }

    private fun acionar(barra: TerminalActionMode, acao: AcaoDeSelecao) {
        composeTestRule.runOnUiThread {
            // The real path: the same one a tap on the button goes through,
            // Menu -> ActionMode.Callback.onActionItemClicked.
            checkNotNull(barra.menuNoAr())
                .performIdentifierAction(Menu.FIRST + acao.ordinal, 0)
        }
        composeTestRule.waitForIdle()
    }

    private fun reabrirEAcionar(barra: TerminalActionMode, acao: AcaoDeSelecao) {
        composeTestRule.runOnUiThread { barra.mostrar() }
        composeTestRule.waitForIdle()
        acionar(barra, acao)
    }
}
