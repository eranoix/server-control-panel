package com.vpsmanager.feature.terminal.selection

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performTouchInput
import com.vpsmanager.feature.terminal.input.ByteSink
import com.vpsmanager.feature.terminal.mouse.MouseEventEncoder
import com.vpsmanager.feature.terminal.mouse.MouseReportGestureController
import com.vpsmanager.feature.terminal.mouse.RoteamentoDeToque
import com.vpsmanager.terminalengine.MouseAction
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** A comfortable margin over `longPressTimeoutMillis` (400–500 ms). */
private const val FOLGA_TOQUE_LONGO_MS = 700L

/** Well inside `doubleTapTimeoutMillis` (300 ms on devices and on the emulator). */
private const val INTERVALO_DE_TOQUE_DUPLO_MS = 60L

/**
 * The defect the app's owner reported: *"in the terminal, when I tap the
 * screen on the writing part, the keyboard should open"*. Tapping the grid did
 * absolutely nothing — the keyboard only came up once, on the system's
 * auto-show when entering the screen, and once dismissed there was no way
 * back.
 *
 * These tests pin down EVERY meaning a single finger can have on the grid,
 * which is where the risk lives: making the short tap work without breaking
 * the long press that already selects text ([canvasDragGestures] +
 * [SelectionGestureController], whose behaviour under a real touch the
 * instrumented `SelectionComposeIndependenceTest` proves), and adding the
 * double/triple tap without breaking either. That is why both recognisers are
 * mounted TOGETHER on the same `Modifier` in every case — testing the tap in
 * isolation would prove nothing about them coexisting.
 */
@RunWith(RobolectricTestRunner::class)
class CanvasTapGestureTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val hitTester = CellHitTester(cellWidthPx = 20f, cellHeightPx = 40f, cols = 40, rows = 40)
    private val selectionHolder = GridSelectionHolder()
    private val selectionController = SelectionGestureController({ hitTester }, selectionHolder)
    private val toques = mutableListOf<Pair<Offset, Int>>()

    private class SinkGravador : ByteSink {
        val enviados = mutableListOf<String>()
        override fun send(bytes: ByteArray) {
            enviados += String(bytes, Charsets.US_ASCII)
        }
    }

    private val sink = SinkGravador()

    /**
     * An encoder that behaves like the native one with tracking ACTIVE — the
     * only situation in which a tap may go to the remote program.
     */
    private val encoderComRastreamento = MouseEventEncoder { action, _, _, _ ->
        val terminador = if (action == MouseAction.RELEASE) 'm' else 'M'
        "\u001b[<0;1;1$terminador".toByteArray(Charsets.US_ASCII)
    }

    /** The same gesture stack [com.vpsmanager.feature.terminal.ui.TerminalRoute] mounts on the grid. */
    private fun montarGrade(policy: RoteamentoDeToque) {
        val mouseController = MouseReportGestureController(encoderComRastreamento, sink)
        val alvoArraste = routeCanvasDrag(policy, selectionController, mouseController)
        val alvoToque = routeCanvasTap(
            policy,
            keyboardTarget = { posicao, taps ->
                if (taps < TOQUE_DUPLO) selectionController.clearSelection()
                toques += posicao to taps
            },
            mouseTarget = mouseController,
        )
        composeRule.setContent {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .canvasDragGestures(alvoArraste)
                    .canvasTapGesture(alvoToque),
            )
        }
    }

    private fun semMouse() = RoteamentoDeToque { false }

    private fun comMouse() = RoteamentoDeToque { true }

    @Test
    fun `toque curto na grade pede o teclado`() {
        montarGrade(semMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.waitForIdle()

        assertEquals("um toque curto na grade tem que pedir o teclado, uma vez", 1, toques.size)
        assertEquals("e ser contado como toque simples", TOQUE_SIMPLES, toques[0].second)
        assertNull("um toque curto não é seleção", selectionHolder.selection)
    }

    @Test
    fun `toque longo continua selecionando e nao pede o teclado`() {
        montarGrade(semMouse())

        // The gesture is split into two blocks with the virtual clock
        // advanced in between: `advanceEventTime` only stamps the injected
        // events' timestamps, and the long-press timer runs as a `delay` on
        // the test's clock.
        composeRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(FOLGA_TOQUE_LONGO_MS)
        }
        composeRule.mainClock.advanceTimeBy(FOLGA_TOQUE_LONGO_MS)

        assertNotNull("o toque longo tem que ancorar a seleção", selectionHolder.selection)
        assertTrue("um toque longo NÃO é toque curto: nada de teclado", toques.isEmpty())

        composeRule.onRoot().performTouchInput {
            moveTo(center + Offset(120f, 80f))
            up()
        }
        composeRule.waitForIdle()

        val selecao = selectionHolder.selection
        assertNotNull("o arraste depois do toque longo tem que manter a seleção viva", selecao)
        assertTrue(
            "soltar o dedo no fim de um arraste de seleção não pode virar um toque curto",
            toques.isEmpty(),
        )
    }

    @Test
    fun `dois toques rapidos no mesmo lugar sao um toque duplo`() {
        montarGrade(semMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            advanceEventTime(INTERVALO_DE_TOQUE_DUPLO_MS)
            down(center)
            up()
        }
        composeRule.waitForIdle()

        assertEquals("os dois toques chegam, na ordem", listOf(TOQUE_SIMPLES, TOQUE_DUPLO), toques.map { it.second })
    }

    @Test
    fun `tres toques rapidos chegam a contagem de linha e nao passam disso`() {
        montarGrade(semMouse())

        repeat(4) {
            composeRule.onRoot().performTouchInput {
                advanceEventTime(INTERVALO_DE_TOQUE_DUPLO_MS)
                down(center)
                up()
            }
        }
        composeRule.waitForIdle()

        assertEquals(
            "acima de três não há gesto definido: o contador satura em vez de criar estados sem significado",
            listOf(TOQUE_SIMPLES, TOQUE_DUPLO, TOQUE_TRIPLO, TOQUE_TRIPLO),
            toques.map { it.second },
        )
    }

    @Test
    fun `dois toques longe um do outro sao dois toques simples`() {
        montarGrade(semMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            advanceEventTime(INTERVALO_DE_TOQUE_DUPLO_MS)
            down(center + Offset(150f, 120f))
            up()
        }
        composeRule.waitForIdle()

        assertEquals(
            "tocar em cantos opostos da tela não é gesto de palavra",
            listOf(TOQUE_SIMPLES, TOQUE_SIMPLES),
            toques.map { it.second },
        )
    }

    @Test
    fun `um toque longo no meio quebra a sequencia de toques`() {
        montarGrade(semMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(FOLGA_TOQUE_LONGO_MS)
        }
        composeRule.mainClock.advanceTimeBy(FOLGA_TOQUE_LONGO_MS)
        composeRule.onRoot().performTouchInput { up() }
        composeRule.onRoot().performTouchInput {
            advanceEventTime(INTERVALO_DE_TOQUE_DUPLO_MS)
            down(center)
            up()
        }
        composeRule.waitForIdle()

        assertEquals(
            "depois de um arraste de seleção o próximo toque recomeça do 1",
            listOf(TOQUE_SIMPLES, TOQUE_SIMPLES),
            toques.map { it.second },
        )
    }

    @Test
    fun `com o programa pedindo mouse o toque vira clique pro programa, nao teclado`() {
        montarGrade(comMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.waitForIdle()

        assertTrue("com mouse ativo o toque pertence ao programa remoto", toques.isEmpty())
        assertEquals(
            "um clique é o par pressiona/solta na mesma célula",
            2,
            sink.enviados.size,
        )
        assertTrue("o primeiro evento é o pressionar (terminador M)", sink.enviados[0].endsWith("M"))
        assertTrue("o segundo é o soltar (terminador m)", sink.enviados[1].endsWith("m"))
    }

    @Test
    fun `sem programa pedindo mouse nenhum byte de mouse e emitido`() {
        // The proof of the defect: at a bash prompt, tapping the grid must
        // not put ONE byte into the stream. That is where the "crazy text"
        // was coming from.
        montarGrade(semMouse())

        composeRule.onRoot().performTouchInput { down(center); up() }
        composeRule.onRoot().performTouchInput {
            down(center)
            advanceEventTime(FOLGA_TOQUE_LONGO_MS)
        }
        composeRule.mainClock.advanceTimeBy(FOLGA_TOQUE_LONGO_MS)
        composeRule.onRoot().performTouchInput {
            moveTo(center + Offset(120f, 80f))
            up()
        }
        composeRule.waitForIdle()

        assertTrue(
            "nem o toque nem o arraste podem virar sequência de mouse quando ninguém pediu mouse",
            sink.enviados.isEmpty(),
        )
    }
}
