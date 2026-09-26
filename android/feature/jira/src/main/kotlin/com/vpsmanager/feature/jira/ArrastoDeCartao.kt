package com.vpsmanager.feature.jira

import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.unit.IntSize
import com.vpsmanager.data.jira.CartaoDoJira

/**
 * The screen edge the finger is resting on during a drag.
 *
 * It exists because on a board with more columns than fit the screen,
 * dragging without it only reaches the neighbouring column: the destination
 * stays out of sight, and the thumb has no way to bring it in. Touching the
 * edge turns the board.
 */
internal enum class BordaDoArrasto { Esquerda, Direita }

/**
 * The card currently being carried by the finger.
 *
 * ## Why a LONG press, and never an immediate drag
 *
 * The column scrolls vertically and the board turns horizontally. A card that
 * starts moving at the first millimetre of finger travel steals both gestures:
 * the column locks up and the board will not turn. The long press separates
 * "I am reading" from "I am moving" with no ambiguity, and it is the same
 * contract every reorderable list on Android uses.
 *
 * ## Why the position is kept in ROOT coordinates
 *
 * The floating card is drawn over everything, outside the column it came
 * from — were it inside, it would vanish the moment it crossed the column's
 * bounds, which is exactly the movement that matters. Drawing on top requires
 * knowing where the card was on the whole screen, not where it was inside the
 * list.
 */
@Stable
internal class EstadoDoArrasto {

    /** The card in flight, or null when nobody is dragging anything. */
    var cartao by mutableStateOf<CartaoDoJira?>(null)
        private set

    /** Which column it left — so undo knows the way back. */
    var colunaDeOrigem by mutableStateOf<String?>(null)
        private set

    /** Top-left corner of the original card, in root coordinates. */
    var origemNaRaiz by mutableStateOf(Offset.Zero)
        private set

    /** How far the finger has travelled since it picked the card up. */
    var deslocamento by mutableStateOf(Offset.Zero)
        private set

    /** Size of the original card — the floating one matches it. */
    var tamanho by mutableStateOf(IntSize.Zero)
        private set

    /** Width of the board area, so we know what counts as an "edge". */
    var larguraDaRaiz by mutableStateOf(0)

    /**
     * Where each column starts and ends, in root coordinates.
     *
     * This is what makes the drag target OBVIOUS: with the three columns on
     * screen at the same time, the destination column is simply the one under
     * the finger. It is deliberately not an observable state map — what is
     * observed is [deslocamento], and [colunaAlvo] recomputes from it.
     */
    private val faixas = LinkedHashMap<String, ClosedFloatingPointRange<Float>>()

    fun registrarColuna(rotulo: String, inicio: Float, fim: Float) {
        faixas[rotulo] = inicio..fim
    }

    val arrastando: Boolean get() = cartao != null

    /**
     * The column under the finger right now, or null if it is outside them
     * all.
     *
     * It reads [deslocamento], which is observable state — so whoever calls
     * this inside a composition recomposes on every movement of the finger,
     * which is exactly what makes the column highlight follow the gesture.
     */
    fun colunaAlvo(): String? {
        if (!arrastando) return null
        val x = centroX
        return faixas.entries.firstOrNull { x in it.value }?.key
    }

    fun pegar(cartao: CartaoDoJira, coluna: String, origem: Offset, tamanho: IntSize) {
        this.cartao = cartao
        this.colunaDeOrigem = coluna
        this.origemNaRaiz = origem
        this.tamanho = tamanho
        this.deslocamento = Offset.Zero
    }

    fun arrastar(delta: Offset) {
        deslocamento += delta
    }

    fun soltar() {
        cartao = null
        colunaDeOrigem = null
        deslocamento = Offset.Zero
        tamanho = IntSize.Zero
    }

    /** The horizontal centre of the floating card, in root coordinates. */
    val centroX: Float get() = origemNaRaiz.x + deslocamento.x + tamanho.width / 2f

    /**
     * Which edge the card is on, if it is on one at all.
     *
     * The band is 18% of the width on each side. Any narrower and the thumb
     * needs precision it does not have while holding a card; any wider and the
     * board turns by itself in the middle of a vertical movement.
     */
    fun borda(): BordaDoArrasto? {
        if (!arrastando || larguraDaRaiz <= 0) return null
        val faixa = larguraDaRaiz * 0.18f
        return when {
            centroX < faixa -> BordaDoArrasto.Esquerda
            centroX > larguraDaRaiz - faixa -> BordaDoArrasto.Direita
            else -> null
        }
    }
}

/**
 * Makes a card pickable by long press.
 *
 * [aoPegar] fires the instant the card is lifted — that is where the haptic
 * happens, because without it there is no way to know the card has been picked
 * up before moving the finger, and the person drags through thin air thinking
 * they are dragging.
 *
 * [aoSoltar] receives the card and decides where it goes; the screen answers
 * with whichever column is in view at the moment the finger lifts.
 *
 * A false [habilitado] turns the whole gesture off — which is what happens
 * during multiple selection in marking mode, where the touch has another
 * owner.
 */
internal fun Modifier.arrastavel(
    estado: EstadoDoArrasto,
    cartao: CartaoDoJira,
    coluna: String,
    habilitado: Boolean,
    aoPegar: () -> Unit,
    aoSoltar: (CartaoDoJira) -> Unit,
): Modifier {
    if (!habilitado) return this
    var origem = Offset.Zero
    var tamanho = IntSize.Zero
    return this
        .onGloballyPositioned { coords ->
            origem = coords.positionInRoot()
            tamanho = coords.size
        }
        .pointerInput(cartao.chave, coluna) {
            detectDragGesturesAfterLongPress(
                onDragStart = {
                    estado.pegar(cartao, coluna, origem, tamanho)
                    aoPegar()
                },
                onDrag = { mudanca, delta ->
                    mudanca.consume()
                    estado.arrastar(delta)
                },
                onDragEnd = {
                    val pego = estado.cartao
                    estado.soltar()
                    if (pego != null) aoSoltar(pego)
                },
                // A cancellation moves NOTHING: the system took the
                // gesture out of our hands (an incoming call, the app going to
                // the background), and moving in that case would mean acting
                // on a gesture that never finished.
                onDragCancel = { estado.soltar() },
            )
        }
}
