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
 * Measures Compose `TerminalCanvas` against `TerminalSurfaceGrid` under the
 * same >=8MB synthetic byte stream (`assets/throughput/throughput.vt`),
 * using in-SDK [FrameMetrics] rather than a new benchmark dependency, so
 * this stays inside the existing dependency set instead of adding a library
 * that would first have to be vetted to come in at all.
 *
 * Requires a real attached `Window` -- `Window.addOnFrameMetricsAvailableListener`
 * has no meaningful shadow, so this class is instrumented-only and cannot be
 * run on the host JVM. It is what the Canvas-versus-SurfaceView rendering
 * verdict rests on.
 *
 * The load is DELIVERED AT FRAME PACE ([CHUNKS_PER_FRAME] chunks per
 * `postOnAnimation`), not in a tight loop. The tight loop was the original
 * design and it measured nothing: the fixture's ~2300 chunks all went in
 * inside the same `onActivity`, with the main thread blocked end to end, and
 * [FrameMetrics] saw ONE frame of 2.3 s. "1 frame, 100% dropped" is not a
 * result about the renderer — it is a measurement of the blocking. At frame
 * pace, each pass writes into the engine and draws the whole grid once, which
 * is exactly the cost in question.
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
                    // TOTAL_DURATION includes waiting for vsync: on an emulator
                    // stuck at 30 Hz it sits glued to 33 ms even with the thread
                    // idle, and does not answer "how much does drawing cost".
                    // DRAW_DURATION is only the time spent recording the draw
                    // operations — the whole-frame cost that is in question.
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
        // Listener removal intentionally omitted: the Activity (and its
        // Window) is destroyed immediately below by ActivityScenario.close(),
        // which is the documented way to stop metric delivery.
        scenario.close()
        metricsThread.quitSafely()
        engine.close()

        val vmHwmAfterKb = readVmHwmKb()
        val snapshot = synchronized(frameDurationsNanos) { frameDurationsNanos.toList() }
        val draws = synchronized(frameDurationsNanos) { drawDurationsNanos.toList() }
        return computeStats(snapshot, draws, peakRssKb = maxOf(vmHwmBeforeKb, vmHwmAfterKb))
    }

    /**
     * Delivers the fixture at frame pace: [CHUNKS_PER_FRAME] chunks per
     * `postOnAnimation`, handing the thread back between batches so that the
     * frame is actually drawn and MEASURED. [onFinished] runs after the last
     * batch.
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

    /** Peak resident set size in KB, from `/proc/self/status` `VmHWM` -- a genuine host-side, no-new-dependency proxy for peak memory. */
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
         * 4 KB chunks written into the engine per frame. With the fixture's
         * ~2300 chunks, 8 per frame give ~288 measured frames (~4.8 s at
         * 60 Hz): a sample large enough for p95/p99 to mean something, and
         * 32 KB of output per frame is a throughput well above what any real
         * command produces.
         */
        const val CHUNKS_PER_FRAME = 8
    }
}
