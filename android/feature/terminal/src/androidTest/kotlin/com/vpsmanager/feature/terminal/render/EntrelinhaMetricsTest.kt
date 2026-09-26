package com.vpsmanager.feature.terminal.render

import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacing
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Line spacing, measured against the device's REAL font.
 *
 * This has to be an instrumented test, not a JVM one: the Android Gradle
 * Plugin stub's `Paint` measures no font at all, and this feature's floor
 * comes entirely out of `Paint.getTextBounds` and `Paint.fontMetrics`. A JVM
 * test here would prove arithmetic, not typography.
 */
@RunWith(AndroidJUnit4::class)
class EntrelinhaMetricsTest {

    private val corpos = listOf(10f * 2.625f, 16f * 2.625f, 28f * 2.625f)

    @Test
    fun alturaDaCelulaEsempreUmInteiroPositivo() {
        // The condition for a 1:1 blit: the atlas slot and the destination
        // rectangle have to be exactly the same size, which is why the height
        // cannot be fractional at any step of the ladder.
        for (corpo in corpos) {
            for (passo in TerminalLineSpacing.entries) {
                val m = computeTerminalCellMetrics(corpo, passo.deltaPx)
                assertTrue(
                    "corpo=$corpo passo=$passo altura=${m.cellHeightPx}",
                    m.cellHeightPx > 0,
                )
            }
        }
    }

    @Test
    fun oCorpoDaLetraNaoMudaComAentrelinha() {
        // The owner asked for "less vertical space", not "smaller type".
        // Before this work `textSizePx` came out of `cellHeightPx`, and
        // tightening the line spacing would have shrunk the glyph with it —
        // this test exists so nobody ties the two together again.
        for (corpo in corpos) {
            val tamanhos = TerminalLineSpacing.entries.map {
                computeTerminalCellMetrics(corpo, it.deltaPx).textSizePx
            }.distinct()
            assertEquals("corpo=$corpo tamanhos=$tamanhos", 1, tamanhos.size)
        }
    }

    @Test
    fun aLarguraDaCelulaNaoMudaComAentrelinha() {
        // Line spacing is vertical. If it touched the width it would change
        // the number of COLUMNS, and therefore where the remote program wraps
        // its text — which is exactly the damage this work went to fix
        // elsewhere.
        for (corpo in corpos) {
            val larguras = TerminalLineSpacing.entries.map {
                computeTerminalCellMetrics(corpo, it.deltaPx).cellWidthPx
            }.distinct()
            assertEquals("corpo=$corpo larguras=$larguras", 1, larguras.size)
        }
    }

    @Test
    fun nenhumPassoCortaLetra() {
        // The floor is not a safety margin: it is the exact height at which
        // the sample's most extreme letter touches the cell's edge. Here the
        // sum is redone from the outside, with the same baseline centring the
        // GlyphAtlas uses, and NOTHING is allowed to cross the edge.
        val amostra = "ÂÊÍÕÜWMbdfhklt gjpqy ç,;_"
        for (corpo in corpos) {
            for (passo in TerminalLineSpacing.entries) {
                val m = computeTerminalCellMetrics(corpo, passo.deltaPx)
                val paint = Paint().apply {
                    typeface = Typeface.MONOSPACE
                    textSize = m.textSizePx
                }
                val fm = paint.fontMetrics
                val base = (m.cellHeightPx - fm.ascent - fm.descent) / 2f
                val caixa = Rect()
                for (c in amostra) {
                    paint.getTextBounds(c.toString(), 0, 1, caixa)
                    val topo = base + caixa.top
                    val fundo = base + caixa.bottom
                    assertTrue(
                        "corpo=$corpo passo=$passo '$c' sai por cima: topo=$topo",
                        topo >= -0.5f,
                    )
                    assertTrue(
                        "corpo=$corpo passo=$passo '$c' sai por baixo: fundo=$fundo altura=${m.cellHeightPx}",
                        fundo <= m.cellHeightPx + 0.5f,
                    )
                }
            }
        }
    }

    @Test
    fun aEscadaEmonotona_ecompactaGanhaLinhaDeVerdade() {
        // Two steps that produce the SAME height are two menu items that do
        // the same thing — and that is how a preference gets a reputation for
        // being broken. It is why the ladder became a whole-pixel delta rather
        // than a fractional multiplier: at small type sizes, 0.90 and 0.95
        // round to the same pixel.
        val corpo = 16f * 2.625f
        val alturas = TerminalLineSpacing.entries.map {
            computeTerminalCellMetrics(corpo, it.deltaPx).cellHeightPx
        }
        assertEquals("a escada tem de ser estritamente crescente: $alturas", alturas.sorted(), alturas)
        assertEquals("nenhum passo pode repetir outro: $alturas", alturas.distinct().size, alturas.size)

        val normal = computeTerminalCellMetrics(corpo, TerminalLineSpacing.NORMAL.deltaPx).cellHeightPx
        val compacta = computeTerminalCellMetrics(corpo, TerminalLineSpacing.COMPACTA.deltaPx).cellHeightPx
        assertTrue("compacta ($compacta) tem de ser menor que normal ($normal)", compacta < normal)
    }

    @Test
    fun normalNaoMudouNada() {
        // A non-regression guard: the grid everyone has always had stays the
        // grid everyone has always had. If this test falls, everyone who never
        // touched the preference has had their screen moved.
        for (corpo in corpos) {
            val m = computeTerminalCellMetrics(corpo, TerminalLineSpacing.NORMAL.deltaPx)
            assertEquals(
                "corpo=$corpo",
                Math.round(corpo),
                m.cellHeightPx,
            )
        }
    }
}
