package com.vpsmanager.feature.terminal.selection

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.drawable.Drawable
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown

/**
 * The handles' touch target. 24 dp is the radius that yields the 48 dp
 * diameter Android's accessibility guidance demands of any control — the
 * DRAWN handle is smaller than that, and a handle that only responds exactly
 * on top of its drawing is, in practice, a handle that does not work.
 */
private val RAIO_DE_TOQUE_DA_ALCA = 24.dp

/**
 * The system's OWN two handle drawables, read from the device theme.
 *
 * They are not this app's icons: `android.R.attr.textSelectHandleLeft`/`Right`
 * are the same resources `TextView` uses, so the terminal's handles have the
 * shape, accent colour and size of the handles on any other text field on the
 * device — including under a manufacturer theme that replaced them.
 */
// `ResourceType`: lint wants a generated `R.styleable.*`, and here the array
// is assembled by hand from PLATFORM attributes — this is how
// `android.widget.Editor` itself looks up the selection handles, and no
// generated `styleable` exists for `android.R.attr` attributes. Suppressing is
// the right answer; changing the pattern would mean giving up the system
// handles.
@SuppressLint("ResourceType")
private class AlcasDoSistema(context: Context) {
    val esquerda: Drawable?
    val direita: Drawable?
    val corDeRealce: Color

    init {
        val atributos = context.theme.obtainStyledAttributes(
            intArrayOf(
                android.R.attr.textSelectHandleLeft,
                android.R.attr.textSelectHandleRight,
                android.R.attr.textColorHighlight,
            ),
        )
        try {
            esquerda = atributos.getDrawable(0)
            direita = atributos.getDrawable(1)
            val realce = atributos.getColor(2, 0)
            corDeRealce = if (realce == 0) Color(0x6633B5E5) else Color(realce)
        } finally {
            atributos.recycle()
        }
    }
}

/**
 * The selection highlight and the two draggable handles, laid over the grid.
 *
 * **This overlay is what was missing entirely.** Before, the selection existed
 * as data (`GridSelectionHolder`) and did not exist as an image: the
 * `TerminalCanvas` never drew any highlight, and there were no handles at all.
 * Selecting was a blind gesture — the operator dragged and only found out what
 * they had caught after pasting it somewhere else.
 *
 * The highlight is drawn OVER the glyphs, in the theme's translucent accent
 * colour (`android.R.attr.textColorHighlight`), rather than underneath as in a
 * `TextView`. Drawing underneath would mean touching the grid rasteriser,
 * which is the hot path of the frame; the system colour already carries alpha
 * and the text stays legible through it.
 *
 * The handles position themselves by Android's convention: the left one hangs
 * from the bottom-left edge of the first cell, the right one from the
 * bottom-right of the last, each offset so that its tip falls exactly on the
 * anchor (this is AOSP's `getHorizontalOffset()`: three quarters of the width
 * for the start handle, one quarter for the end one).
 */
@Composable
fun SelectionOverlay(
    selectionState: State<GridSelection?>,
    hitTesterProvider: () -> CellHitTester,
    aoArrastarAlca: (SelectionHandle, Offset) -> Unit,
    aoTerminarArrasteDeAlca: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val alcas = remember(context) { AlcasDoSistema(context) }
    val raioPx = with(LocalDensity.current) { RAIO_DE_TOQUE_DA_ALCA.toPx() }

    // The magnifier magnifies the window's SURFACE, so the host has to be the
    // view containing the drawn grid — the Compose root, not this layer.
    val raizDoCompose = LocalView.current
    val lupa = remember(raizDoCompose) { LupaDaAlca(raizDoCompose) }
    DisposableEffect(lupa) { onDispose { lupa.descartar() } }

    // The gesture's coordinates are local to this layer; the magnifier wants
    // them relative to the root. Without this conversion the magnifier
    // magnifies the wrong place as soon as the grid does not start at the top
    // of the window (with the key row open, for instance).
    var coordenadas by remember { mutableStateOf<LayoutCoordinates?>(null) }

    Canvas(
        modifier = modifier
            .fillMaxSize()
            .clipToBounds()
            .onGloballyPositioned { coordenadas = it }
            .pointerInput(hitTesterProvider, raioPx) {
                awaitEachGesture {
                    // Never requires "unconsumed": this detector has to see
                    // the touch before deciding whether it is its own, and only
                    // then consumes. Consuming before knowing would steal from
                    // the grid every touch that was not on a handle.
                    val down = awaitFirstDown(requireUnconsumed = false)
                    val selecao = selectionState.value ?: return@awaitEachGesture
                    val ancoras = handleAnchors(selecao, hitTesterProvider())
                    val alca = handleAt(down.position, ancoras, raioPx) ?: return@awaitEachGesture

                    // From here on the gesture is OURS. Consuming every event
                    // is what stops the grid's tap recogniser and its
                    // drag-after-long-press recogniser from acting on the same
                    // finger — both give up once they see the event consumed.
                    down.consume()
                    while (true) {
                        val evento = awaitPointerEvent()
                        val mudanca = evento.changes.firstOrNull { it.id == down.id } ?: break
                        mudanca.consume()
                        if (!mudanca.pressed) break
                        aoArrastarAlca(alca, mudanca.position)
                        mostrarLupa(lupa, coordenadas, hitTesterProvider(), mudanca.position)
                    }
                    lupa.esconder()
                    aoTerminarArrasteDeAlca()
                }
            },
    ) {
        val selecao = selectionState.value ?: return@Canvas
        val hitTester = hitTesterProvider()
        desenharRealce(selecao, hitTester, alcas.corDeRealce)
        desenharAlcas(selecao, hitTester, alcas)
    }
}

private fun DrawScope.desenharRealce(
    selecao: GridSelection,
    hitTester: CellHitTester,
    cor: Color,
) {
    val ordenada = emOrdemDeLeitura(selecao)
    selectionRowRanges(selecao, hitTester.cols).forEachIndexed { indice, faixa ->
        val linha = ordenada.startRow + indice
        val primeira = hitTester.cellRect(linha, faixa.first)
        val ultima = hitTester.cellRect(linha, faixa.last)
        drawRect(
            color = cor,
            topLeft = Offset(primeira.left, primeira.top),
            size = Size(ultima.right - primeira.left, primeira.height),
        )
    }
}

private fun DrawScope.desenharAlcas(
    selecao: GridSelection,
    hitTester: CellHitTester,
    alcas: AlcasDoSistema,
) {
    val ancoras = handleAnchors(selecao, hitTester)
    drawIntoCanvas { canvas ->
        alcas.esquerda?.let { desenho ->
            val largura = desenho.intrinsicWidth
            val altura = desenho.intrinsicHeight
            val esquerda = (ancoras.inicio.x - largura * 3 / 4f).toInt()
            val topo = ancoras.inicio.y.toInt()
            desenho.setBounds(esquerda, topo, esquerda + largura, topo + altura)
            desenho.draw(canvas.nativeCanvas)
        }
        alcas.direita?.let { desenho ->
            val largura = desenho.intrinsicWidth
            val altura = desenho.intrinsicHeight
            val esquerda = (ancoras.fim.x - largura / 4f).toInt()
            val topo = ancoras.fim.y.toInt()
            desenho.setBounds(esquerda, topo, esquerda + largura, topo + altura)
            desenho.draw(canvas.nativeCanvas)
        }
    }
}

/**
 * Puts the magnifier over the cell the finger is choosing.
 *
 * The Y goes to the CENTRE OF THE CELL, not to the finger: that is what stops
 * the magnifier trembling vertically as the hand wavers, and what makes it
 * show a whole line instead of half of one above and half of one below. Same
 * behaviour as `TextView`, and for the same reason — whoever is dragging needs
 * to read the line.
 *
 * With [coordenadas] not yet measured there is no way to convert to the root,
 * and magnifying the wrong place is worse than not magnifying at all.
 */
private fun mostrarLupa(
    lupa: LupaDaAlca,
    coordenadas: LayoutCoordinates?,
    hitTester: CellHitTester,
    posicao: Offset,
) {
    val coords = coordenadas ?: return
    val celula = hitTester.hitTest(posicao)
    val retangulo = hitTester.cellRect(celula.row, celula.col)
    val naRaiz = coords.localToRoot(Offset(posicao.x, retangulo.center.y))
    lupa.mostrar(naRaiz.x, naRaiz.y)
}
