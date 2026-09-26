package com.vpsmanager.feature.terminal.render

import android.content.Context
import android.graphics.Canvas
import android.util.AttributeSet
import android.view.SurfaceHolder
import android.view.SurfaceView
import com.vpsmanager.terminalengine.CellSnapshot
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

/**
 * SurfaceView-based alternative to [TerminalCanvas], built as a genuine
 * competing implementation for the Task 7 measured verdict rather than a
 * strawman: a dedicated render thread blits frames onto the `Surface` off
 * the UI thread via `lockCanvas`/`unlockCanvasAndPost`, using the exact
 * same [buildRowDrawOps] + [rasterizeRow] + [GlyphAtlas] pipeline
 * [TerminalCanvas] uses. That shared pipeline is what makes the two
 * renderers a fair, apples-to-apples comparison in
 * `:benchmark`'s `GridThroughputBenchmark`: the measured difference is
 * renderer architecture (Compose recomposition/draw-phase invalidation vs
 * an independent render thread with its own frame pacing), not different
 * drawing code.
 *
 * [postSnapshot] is the entire thread-safety contract: safe to call from
 * any thread, matching [TerminalEngine.snapshot]'s own free-threaded
 * handoff. The render thread only ever reads the latest posted value.
 */
class TerminalSurfaceGrid @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
) : SurfaceView(context, attrs), SurfaceHolder.Callback {

    private val latestSnapshot = AtomicReference<CellSnapshot?>(null)
    private val running = AtomicBoolean(false)
    private var renderThread: Thread? = null

    var cellWidthPx: Float = 16f
    var cellHeightPx: Float = 28f
    var glyphAtlas: GlyphAtlas = GlyphAtlas(cellWidthPx.toInt(), cellHeightPx.toInt())
    var defaultFg: Int = PaletaTerminalEscura.defaultFg
    var defaultBg: Int = PaletaTerminalEscura.defaultBg

    /** Light-theme legibility guard — see [TerminalPalette]. */
    var minLumaDelta: Int = PaletaTerminalEscura.minLumaDelta

    /** Set by the render loop after each completed frame; read-only for callers/benchmarks. */
    @Volatile var framesRendered: Long = 0
        private set

    init {
        holder.addCallback(this)
    }

    fun postSnapshot(snapshot: CellSnapshot) {
        latestSnapshot.set(snapshot)
    }

    override fun surfaceCreated(holder: SurfaceHolder) {
        running.set(true)
        renderThread = Thread(::renderLoop, "TerminalSurfaceGrid-render").apply { start() }
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) = Unit

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        running.set(false)
        renderThread?.join(1_000)
        renderThread = null
    }

    private fun renderLoop() {
        var lastDrawn: CellSnapshot? = null
        while (running.get()) {
            val snapshot = latestSnapshot.get()
            if (snapshot == null || snapshot === lastDrawn) {
                Thread.sleep(1)
                continue
            }
            lastDrawn = snapshot
            drawFrame(snapshot)
            framesRendered++
        }
    }

    private fun drawFrame(snapshot: CellSnapshot) {
        val canvas: Canvas = holder.lockCanvas() ?: return
        try {
            // The same frame-drawing path as [TerminalCanvas] — including
            // here, where `lockCanvas` returns a buffer from a circular queue
            // (its content is from two or three frames back, not the last
            // one), which makes any "unchanged row" cache equally invalid.
            // See the KDoc on [rasterizeFrame].
            rasterizeFrame(
                canvas = canvas,
                cols = snapshot.cols,
                rows = snapshot.rows,
                cellAt = snapshot::cellAt,
                cellWidthPx = cellWidthPx,
                cellHeightPx = cellHeightPx,
                glyphAtlas = glyphAtlas,
                defaultFg = defaultFg,
                defaultBg = defaultBg,
                minLumaDelta = minLumaDelta,
            )
        } finally {
            holder.unlockCanvasAndPost(canvas)
        }
    }
}
