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
import com.vpsmanager.data.terminal.TerminalTicketSource
import com.vpsmanager.data.terminal.TerminalWebSocket
import com.vpsmanager.data.terminal.TerminalWebSocketFactory
import com.vpsmanager.data.terminal.TerminalWebSocketListener
import com.vpsmanager.data.terminal.WsTicketResult
import com.vpsmanager.feature.terminal.render.GlyphAtlas
import com.vpsmanager.feature.terminal.render.TerminalCanvas
import com.vpsmanager.feature.terminal.transport.TerminalSocketClient
import com.vpsmanager.terminalengine.CellSnapshot
import com.vpsmanager.terminalengine.TerminalEngine
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.Test
import org.junit.runner.RunWith
import java.io.BufferedReader
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.math.roundToLong

/**
 * [GridThroughputBenchmark] proved the renderer alone holds frame budget
 * under load; this drives the SAME >=8MB fixture through the actual live
 * path the app ships — `TerminalSocketClient`'s `onBytes` callback into
 * `TerminalEngine.write`, exactly as `TerminalRoute`/`TerminalViewModel`
 * wire it — so a regression in the transport-to-render seam, not just the
 * renderer, would show up here too.
 *
 * A fake [TerminalWebSocketFactory] replays the fixture as `onBinaryMessage`
 * calls instead of opening a real socket, and `TerminalSocketClient`'s
 * [CoroutineScope] uses [Dispatchers.Unconfined] so each delivery runs on the
 * thread that fires it — the same thread the real [TerminalSocketClient]
 * delivers bytes on relative to its own scope. No real network, WS server, or
 * extra coroutine dispatcher dependency is needed to prove the seam holds.
 *
 * Delivery is AT FRAME PACE ([CHUNKS_PER_FRAME] WS frames per
 * `postOnAnimation`), for the same reason as [GridThroughputBenchmark] in the
 * sibling file: the original synchronous burst blocked the main thread from
 * start to finish and [FrameMetrics] saw a handful of giant frames — a
 * measurement of the blocking, not of the renderer. It is also more faithful:
 * a real server delivers a burst of output as a SEQUENCE of WS frames over
 * time, not as a single write.
 *
 * Instrumented-only (needs a real attached `Window` —
 * `Window.addOnFrameMetricsAvailableListener` has no meaningful shadow).
 */
@RunWith(AndroidJUnit4::class)
class LiveThroughputBenchmark {

    private val cols = 120
    private val rows = 40
    private val chunkSize = 4096
    private val cellWidthPx = 16f
    private val cellHeightPx = 28f

    @Test
    fun liveTransportToCanvas_frameMetrics() {
        val stats = measure()
        report(stats)
    }

    private fun measure(): FrameStats {
        val fixtureBytes = readFixture()
        val engine = TerminalEngine.create(cols, rows)
        val frameDurationsNanos = mutableListOf<Long>()
        val drawDurationsNanos = mutableListOf<Long>()
        val metricsThread = HandlerThread("live-frame-metrics").apply { start() }
        val metricsHandler = Handler(metricsThread.looper)
        val vmHwmBeforeKb = readVmHwmKb()
        val socketScope = CoroutineScope(Dispatchers.Unconfined + SupervisorJob())

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

            val socketClient = TerminalSocketClient(
                name = "bench",
                ticketSource = object : TerminalTicketSource {
                    override suspend fun wsTicket(name: String) =
                        WsTicketResult.Success(ticket = "bench-ticket", expiresIn = 60)
                },
                webSocketFactory = TerminalWebSocketFactory { _, wsListener ->
                    pacedFixtureSocket(activity.root, fixtureBytes, wsListener) {
                        activity.window.decorView.postDelayed({ doneLatch.countDown() }, SETTLE_MILLIS)
                    }
                },
                wsBaseUrl = "wss://benchmark.invalid",
                scope = socketScope,
                onBytes = { bytes ->
                    engine.write(bytes)
                    snapshotState.value = engine.snapshot()
                },
            )
            socketClient.connect()
        }

        doneLatch.await(SETTLE_MILLIS + 30_000, TimeUnit.MILLISECONDS)
        scenario.close()
        socketScope.cancel()
        metricsThread.quitSafely()
        engine.close()

        val vmHwmAfterKb = readVmHwmKb()
        val snapshot = synchronized(frameDurationsNanos) { frameDurationsNanos.toList() }
        val draws = synchronized(frameDurationsNanos) { drawDurationsNanos.toList() }
        return computeStats(snapshot, draws, peakRssKb = maxOf(vmHwmBeforeKb, vmHwmAfterKb))
    }

    /**
     * Fake `TerminalWebSocket`: delivers the fixture as a sequence of
     * `onBinaryMessage` calls, [CHUNKS_PER_FRAME] per animation frame — which
     * is how a real server delivers a burst of output: several WS frames over
     * time. Handing the main thread back between batches is what lets the frame
     * be drawn and measured. It never closes by itself: this benchmark measures
     * the burst, not the shutdown.
     */
    private fun pacedFixtureSocket(
        host: android.view.View,
        bytes: ByteArray,
        listener: TerminalWebSocketListener,
        onFinished: () -> Unit,
    ): TerminalWebSocket {
        listener.onOpen()
        var offset = 0
        lateinit var step: Runnable
        step = Runnable {
            var inThisFrame = 0
            while (offset < bytes.size && inThisFrame < CHUNKS_PER_FRAME) {
                val end = minOf(offset + chunkSize, bytes.size)
                listener.onBinaryMessage(bytes.copyOfRange(offset, end))
                offset = end
                inThisFrame++
            }
            if (offset < bytes.size) host.postOnAnimation(step) else onFinished()
        }
        host.postOnAnimation(step)
        return object : TerminalWebSocket {
            override fun sendBytes(bytes: ByteArray) = true
            override fun sendText(text: String) = true
            override fun close(code: Int, reason: String) = true
        }
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

    private fun report(stats: FrameStats) {
        println(
            "[LiveThroughputBenchmark] TerminalSocketClient -> TerminalCanvas: frames=${stats.frameCount} " +
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
        /** 60Hz frame budget; a frame taking longer than this is counted as dropped. Mirrors GridThroughputBenchmark. */
        const val FRAME_BUDGET_MILLIS = 16L
        const val SETTLE_MILLIS = 2_000L

        /** WS frames delivered per animation frame. Same number as GridThroughputBenchmark, so the two results compare. */
        const val CHUNKS_PER_FRAME = 8
    }
}
