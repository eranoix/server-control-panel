package dev.servercontrolpanel.feature.terminal.render

import android.content.Context
import android.graphics.Canvas
import android.util.AttributeSet
import android.view.SurfaceHolder
import android.view.SurfaceView
import dev.servercontrolpanel.terminalengine.CellSnapshot
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

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
    var defaultFg: Int = DarkTerminalPalette.defaultFg
    var defaultBg: Int = DarkTerminalPalette.defaultBg

    var minLumaDelta: Int = DarkTerminalPalette.minLumaDelta

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
