package com.vpsmanager.benchmark

import android.os.Handler
import android.os.HandlerThread
import android.view.FrameMetrics
import android.view.Window
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.platform.ComposeView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.vpsmanager.feature.terminal.render.GlyphAtlas
import com.vpsmanager.feature.terminal.render.TerminalCanvas
import com.vpsmanager.feature.terminal.render.TerminalSurfaceGrid
import com.vpsmanager.terminalengine.CellSnapshot
import com.vpsmanager.terminalengine.TerminalEngine
import org.junit.Test
import org.junit.runner.RunWith
import java.io.BufferedReader
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.math.roundToLong

/**
 * Compares Compose `TerminalCanvas` with `TerminalSurfaceGrid` on the same
 * 8MB+ synthetic stream, using SDK [FrameMetrics] (no benchmark library).
 *
 * Instrumented only: frame metrics need a real attached `Window`.
 *
 * Input is fed at frame pace ([CHUNKS_PER_FRAME] chunks per `postOnAnimation`);
 * a tight loop would block the main thread and produce a single huge frame.
 */
@RunWith(AndroidJUnit4::class)
class GridThroughputBenchmark {

    private val cols = 120
    private val rows = 40
    private val chunkSize = 4096
    private val cellWidthPx = 16f
    private val cellHeightPx = 28f

    @Test
    fun canvasRenderer_frameMetrics() {
        val stats = measureRenderer(useSurfaceView = false)
        report("TerminalCanvas (Compose)", stats)
    }

    @Test
    fun surfaceViewRenderer_frameMetrics() {
        val stats = measureRenderer(useSurfaceView = true)
        report("TerminalSurfaceGrid (SurfaceView)", stats)
    }

    private fun measureRenderer(useSurfaceView: Boolean): FrameStats {
        val fixtureBytes = readFixture()
        val engine = TerminalEngine.create(cols, rows)
        val frameDurationsNanos = mutableListOf<Long>()
        val drawDurationsNanos = mutableListOf<Long>()
        val metricsThread = HandlerThread("frame-metrics").apply { start() }
        val metricsHandler = Handler(metricsThread.looper)
        val vmHwmBeforeKb = readVmHwmKb()

        val scenario = ActivityScenario.launch(BenchmarkHostActivity::class.java)
        val doneLatch = CountDownLatch(1)

        scenario.onActivity { activity ->
            val listener = Window.OnFrameMetricsAvailableListener { _, frameMetrics, _ ->
                synchronized(frameDurationsNanos) {
                    frameDurationsNanos += frameMetrics.getMetric(FrameMetrics.TOTAL_DURATION)
                    // TOTAL_DURATION includes vsync waits (33 ms on a 30 Hz emulator);
                    // DRAW_DURATION isolates the drawing cost being measured.
                    drawDurationsNanos += frameMetrics.getMetric(FrameMetrics.DRAW_DURATION)
                }
            }
            activity.window.addOnFrameMetricsAvailableListener(listener, metricsHandler)

            val snapshotState = mutableStateOf<CellSnapshot?>(null)
            val glyphAtlas = GlyphAtlas(cellWidthPx.toInt(), cellHeightPx.toInt())

            val publish: (CellSnapshot) -> Unit
            if (useSurfaceView) {
                val surfaceGrid = TerminalSurfaceGrid(activity).apply {
                    this.cellWidthPx = this@GridThroughputBenchmark.cellWidthPx
                    this.cellHeightPx = this@GridThroughputBenchmark.cellHeightPx
                    this.glyphAtlas = glyphAtlas
                }
                activity.root.addView(surfaceGrid)
                publish = { snapshot -> surfaceGrid.postSnapshot(snapshot) }
            } else {
                val composeView = ComposeView(activity).apply {
                    setContent {
                        TerminalCanvas(
                            snapshotState = snapshotState,
                            cellWidthPx = cellWidthPx,
                            cellHeightPx = cellHeightPx,
                            glyphAtlas = glyphAtlas,
                        )
                    }
                }
                activity.root.addView(composeView)
                publish = { snapshot -> snapshotState.value = snapshot }
            }

            feedPaced(activity.root, engine, fixtureBytes, publish) {
                activity.window.decorView.postDelayed({ doneLatch.countDown() }, SETTLE_MILLIS)
            }
        }

        doneLatch.await(SETTLE_MILLIS + 30_000, TimeUnit.MILLISECONDS)
        // No listener removal needed: close() destroys the Window, stopping delivery.
        scenario.close()
        metricsThread.quitSafely()
        engine.close()

        val vmHwmAfterKb = readVmHwmKb()
        val snapshot = synchronized(frameDurationsNanos) { frameDurationsNanos.toList() }
        val draws = synchronized(frameDurationsNanos) { drawDurationsNanos.toList() }
        return computeStats(snapshot, draws, peakRssKb = maxOf(vmHwmBeforeKb, vmHwmAfterKb))
    }

    /**
     * Feeds [CHUNKS_PER_FRAME] chunks per `postOnAnimation`, yielding between
     * batches so each frame is drawn and measured. [onFinished] runs after the last batch.
     */
    private fun feedPaced(
        host: android.view.View,
        engine: TerminalEngine,
        bytes: ByteArray,
        publish: (CellSnapshot) -> Unit,
        onFinished: () -> Unit,
    ) {
        var offset = 0
        lateinit var step: Runnable
        step = Runnable {
            var inThisFrame = 0
            while (offset < bytes.size && inThisFrame < CHUNKS_PER_FRAME) {
                val end = minOf(offset + chunkSize, bytes.size)
                engine.write(bytes.copyOfRange(offset, end))
                offset = end
                inThisFrame++
            }
            publish(engine.snapshot())
            if (offset < bytes.size) host.postOnAnimation(step) else onFinished()
        }
        host.postOnAnimation(step)
    }

    private fun readFixture(): ByteArray {
        val context = InstrumentationRegistry.getInstrumentation().context
        return context.assets.open("throughput/throughput.vt").use { it.readBytes() }
    }

    /** Peak resident set size in KB, from `VmHWM` in `/proc/self/status`. */
    private fun readVmHwmKb(): Long {
        return try {
            File("/proc/self/status").bufferedReader().use { reader: BufferedReader ->
                reader.lineSequence()
                    .firstOrNull { it.startsWith("VmHWM:") }
                    ?.trim()
                    ?.split(Regex("\\s+"))
                    ?.getOrNull(1)
                    ?.toLongOrNull()
            } ?: -1L
        } catch (_: Exception) {
            -1L
        }
    }

    private fun computeStats(durationsNanos: List<Long>, drawNanos: List<Long>, peakRssKb: Long): FrameStats {
        if (durationsNanos.isEmpty()) {
            return FrameStats(0, 0, 0, 0.0, peakRssKb, 0, 0, 0, 0)
        }
        val sortedDraw = drawNanos.sorted()
        val sorted = durationsNanos.sorted()
        val budgetNanos = FRAME_BUDGET_MILLIS * 1_000_000L
        val dropped = sorted.count { it > budgetNanos }
        return FrameStats(
            p50Millis = percentile(sorted, 0.50),
            p95Millis = percentile(sorted, 0.95),
            p99Millis = percentile(sorted, 0.99),
            droppedFramePct = 100.0 * dropped / sorted.size,
            peakRssKb = peakRssKb,
            frameCount = sorted.size,
            drawP50Millis = if (sortedDraw.isEmpty()) 0 else percentile(sortedDraw, 0.50),
            drawP95Millis = if (sortedDraw.isEmpty()) 0 else percentile(sortedDraw, 0.95),
            drawP99Millis = if (sortedDraw.isEmpty()) 0 else percentile(sortedDraw, 0.99),
        )
    }

    private fun percentile(sortedNanos: List<Long>, p: Double): Long {
        val index = ((sortedNanos.size - 1) * p).roundToLong().toInt().coerceIn(0, sortedNanos.size - 1)
        return sortedNanos[index] / 1_000_000L
    }

    private fun report(label: String, stats: FrameStats) {
        println(
            "[GridThroughputBenchmark] $label: frames=${stats.frameCount} " +
                "p50=${stats.p50Millis}ms p95=${stats.p95Millis}ms p99=${stats.p99Millis}ms " +
                "dropped=${"%.2f".format(stats.droppedFramePct)}% peakRssKb=${stats.peakRssKb} " +
                "draw_p50=${stats.drawP50Millis}ms draw_p95=${stats.drawP95Millis}ms draw_p99=${stats.drawP99Millis}ms",
        )
    }

    private data class FrameStats(
        val p50Millis: Long,
        val p95Millis: Long,
        val p99Millis: Long,
        val droppedFramePct: Double,
        val peakRssKb: Long,
        val frameCount: Int,
        val drawP50Millis: Long,
        val drawP95Millis: Long,
        val drawP99Millis: Long,
    )

    private companion object {
        /** 60Hz frame budget; a frame taking longer than this is counted as dropped. */
        const val FRAME_BUDGET_MILLIS = 16L
        const val SETTLE_MILLIS = 2_000L

        /**
         * 4 KB chunks per frame. 8 gives about 288 measured frames, enough for
         * meaningful p95/p99, and 32 KB per frame exceeds any real command's output.
         */
        const val CHUNKS_PER_FRAME = 8
    }
}
