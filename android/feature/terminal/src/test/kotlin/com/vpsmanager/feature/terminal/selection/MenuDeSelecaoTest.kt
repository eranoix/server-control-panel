package com.vpsmanager.feature.terminal.selection

import android.content.Intent
import android.view.Menu
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The HIERARCHY of the floating selection bar — what stays in sight and what
 * goes behind the three dots.
 *
 * Runs on the JVM, with no emulator. [itensDaBarra] is pure data — the
 * system's labels (`android.R.string.*`) are `static final int` with a
 * `ConstantValue` in `android.jar`, so the compiler inlines them as literals
 * and nothing consults Android at runtime. Robolectric only enters through the
 * `Intent` tests, which need the real implementation (`putExtra`,
 * `createChooser`) rather than the stub that throws.
 *
 * What this test does NOT prove is the other side of the contract: that
 * Android really does translate `SHOW_AS_ACTION_ALWAYS`/`NEVER` into
 * bar/overflow. That is framework behaviour, documented in [Destaque] from the
 * AOSP source, and the proof is `TerminalActionModeTest` (a real menu, on a
 * device) plus the emulator screenshot.
 */
@RunWith(RobolectricTestRunner::class)
class MenuDeSelecaoTest {

    private fun item(acao: AcaoDeSelecao) = itensDaBarra().single { it.acao == acao }

    @Test
    fun copiarEColarSaoAsDuasAcoesDaBarra() {
        val naBarra = itensDaBarra().filter { it.destaque == Destaque.BARRA }.map { it.acao }

        assertEquals(
            "o pedido é literal: copiar e colar sempre aparecem, o resto vai pro estouro",
            listOf(AcaoDeSelecao.COPIAR, AcaoDeSelecao.COLAR),
            naBarra,
        )
    }

    @Test
    fun todoOResto_vaiProMenuDeEstouro() {
        val noEstouro = itensDaBarra().filter { it.destaque == Destaque.ESTOURO }.map { it.acao }

        assertEquals(
            // Share before Select all is AOSP's own order (`_SHARE = 7`
            // comes before `_SELECT_ALL = 8`); the item that is only ours goes
            // last.
            listOf(
                AcaoDeSelecao.COMPARTILHAR,
                AcaoDeSelecao.SELECIONAR_TUDO,
                AcaoDeSelecao.ENVIAR_PRO_TERMINAL,
            ),
            noEstouro,
        )
    }

    @Test
    fun aOrdemDosItensEhAMesmaDeUmCampoDeTextoDoAndroid() {
        // The numbers are AOSP's `Editor.ACTION_MODE_MENU_ITEM_ORDER_*`:
        // COPY=5, PASTE=6, SHARE=7, SELECT_ALL=8. Reusing them is what makes
        // the terminal's bar present its actions in the same sequence as any
        // other field on the device.
        assertEquals(5, AcaoDeSelecao.COPIAR.ordem)
        assertEquals(6, AcaoDeSelecao.COLAR.ordem)
        assertEquals(7, AcaoDeSelecao.COMPARTILHAR.ordem)
        assertEquals(8, AcaoDeSelecao.SELECIONAR_TUDO.ordem)
        // What is only ours comes after AOSP's last item (=11).
        assertTrue(AcaoDeSelecao.ENVIAR_PRO_TERMINAL.ordem > 11)
        // And anything from an outside app comes after everything of ours.
        assertTrue(itensDaBarra().all { it.ordem < ORDEM_DE_OUTROS_APPS })
    }

    @Test
    fun aBarraNaoCresceSemQueAlguemDecida() {
        // A crowded bar is as bad as an empty one: with too many items the
        // main panel fills up by width and the `⋮` stops being predictable.
        // This test is the gate — changing the set forces a review of the
        // hierarchy along with it.
        assertEquals(2, itensDaBarra().count { it.destaque == Destaque.BARRA })
        assertEquals(5, itensDaBarra().size)
    }

    @Test
    fun copiarEhOPrimeiroItem_porqueEhOQueMaisSeUsa() {
        // It is also the safety net for `layoutMainPanelItems`: it promotes
        // the FIRST item to the main panel even if that item asked for
        // overflow. With Copy at the front, that exception never puts a
        // secondary item on the bar by accident.
        assertEquals(AcaoDeSelecao.COPIAR, itensDaBarra().first().acao)
    }

    @Test
    fun aOrdemDeclaradaEhAOrdemDoMenu() {
        // `FloatingToolbar.mMenuItemComparator` breaks ties by `getOrder()`
        // within each showAsAction class — if the order is not strictly
        // increasing, the bar shuffles itself.
        val ordens = itensDaBarra().map { it.ordem }

        assertEquals(ordens.sorted(), ordens)
        assertEquals("sem ordens repetidas", ordens.size, ordens.toSet().size)
    }

    @Test
    fun cadaItemTemUmIdProprioEEstavel() {
        val ids = itensDaBarra().map { it.id }

        assertEquals("id repetido faria um clique acionar a ação errada", ids.size, ids.toSet().size)
        assertTrue("ids do menu começam em Menu.FIRST", ids.all { it >= Menu.FIRST })
    }

    @Test
    fun idDeVoltaParaAcao_paraODespachoDoClique() {
        for (esperada in AcaoDeSelecao.entries) {
            assertEquals(esperada, acaoDoItem(item(esperada).id))
        }
    }

    @Test
    fun idDesconhecido_naoViraAcaoNenhuma() {
        // The bar may host items that are not ours; an id outside the range
        // has to return null so the callback answers `false` and lets the
        // system handle it, rather than firing the last action in the list.
        assertNull(acaoDoItem(Menu.FIRST - 1))
        assertNull(acaoDoItem(Menu.FIRST + AcaoDeSelecao.entries.size))
        assertNull(acaoDoItem(0))
    }

    @Test
    fun colarNaoEhCondicional_elePermaneceNaBarraSemQualquerConsulta() {
        // The test that protects the decision: `itensDaBarra()` takes no
        // state at all. If someone one day wants to hide "Paste" with an empty
        // clipboard, they will have to change the signature — and then they
        // will read the comment about `getVisibleAndEnabledMenuItems` and
        // about Android 12's notice.
        assertNotNull(item(AcaoDeSelecao.COLAR))
        assertEquals(Destaque.BARRA, item(AcaoDeSelecao.COLAR).destaque)
    }

    @Test
    fun rotulosDoSistemaOndeExistem_nossosSoOndeNaoExistem() {
        // Copy/Paste/Select all come from Android itself, already translated
        // into the device's language. Share has no public
        // `android.R.string.share` (checked in the SDK 37 android.jar), so it
        // is ours.
        assertEquals(android.R.string.copy, item(AcaoDeSelecao.COPIAR).tituloDoSistema)
        assertEquals(android.R.string.paste, item(AcaoDeSelecao.COLAR).tituloDoSistema)
        assertEquals(android.R.string.selectAll, item(AcaoDeSelecao.SELECIONAR_TUDO).tituloDoSistema)

        assertEquals("Share", item(AcaoDeSelecao.COMPARTILHAR).tituloProprio)
        assertEquals("Send to terminal", item(AcaoDeSelecao.ENVIAR_PRO_TERMINAL).tituloProprio)
    }

    // ---- Other apps' actions (ACTION_PROCESS_TEXT) ----

    @Test
    fun idDeOutroApp_naoColideComOsNossos() {
        // The defect this separate range avoids: "Translate" firing "Send to
        // the terminal" because the ids ran into each other.
        val nossos = itensDaBarra().map { it.id }.toSet()
        val deOutros = List(20) { AcaoDeOutroApp("app $it", "p$it", "c$it").id(it) }

        assertTrue(nossos.none { it in deOutros })
        assertTrue("id de outro app nunca vira ação nossa", deOutros.all { acaoDoItem(it) == null })
    }

    @Test
    fun indiceDeOutroApp_soReconheceOQueEstaNoMenu() {
        assertEquals(0, indiceDeOutroApp(ID_BASE_DE_OUTROS_APPS, quantidade = 2))
        assertEquals(1, indiceDeOutroApp(ID_BASE_DE_OUTROS_APPS + 1, quantidade = 2))
        // Outside the assembled range: the app may have been uninstalled
        // between building the menu and the click. `null` makes the callback
        // return false rather than throwing an IndexOutOfBounds.
        assertNull(indiceDeOutroApp(ID_BASE_DE_OUTROS_APPS + 2, quantidade = 2))
        assertNull(indiceDeOutroApp(ID_BASE_DE_OUTROS_APPS - 1, quantidade = 2))
        assertNull(indiceDeOutroApp(Menu.FIRST, quantidade = 2))
    }

    @Test
    fun intentDeProcessarTexto_carregaOTextoEDizQueEhSoLeitura() {
        val acao = AcaoDeOutroApp("Traduzir", "com.exemplo.tradutor", "com.exemplo.tradutor.Tela")
        val intent = intentDeProcessarTexto(acao, "ls: invalid option -- '2'")

        assertEquals(Intent.ACTION_PROCESS_TEXT, intent.action)
        assertEquals("text/plain", intent.type)
        assertEquals(acao.pacote, intent.component?.packageName)
        assertEquals(acao.classe, intent.component?.className)
        assertEquals("ls: invalid option -- '2'", intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT))
        // Read-only, because the grid is a projection of what the remote
        // program printed — no outside app may rewrite it.
        assertTrue(intent.getBooleanExtra(Intent.EXTRA_PROCESS_TEXT_READONLY, false))
    }

    @Test
    fun intentDeCompartilhar_ehOEnvioDeTextoSimplesDoSistema() {
        val chooser = intentDeCompartilharTexto("erro do build")

        assertEquals(Intent.ACTION_CHOOSER, chooser.action)
        val enviado = chooser.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
        assertEquals(Intent.ACTION_SEND, enviado?.action)
        assertEquals("text/plain", enviado?.type)
        assertEquals("erro do build", enviado?.getStringExtra(Intent.EXTRA_TEXT))
    }

    @Test
    fun umItemNuncaTemOsDoisRotulos_nemNenhum() {
        val erro = runCatching {
            ItemDaBarra(AcaoDeSelecao.COPIAR, Destaque.BARRA)
        }.exceptionOrNull()

        assertTrue("item sem rótulo nenhum apareceria em branco na barra", erro is IllegalArgumentException)
    }
}
