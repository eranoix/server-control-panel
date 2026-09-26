package dev.servercontrolpanel.benchmark

import android.os.Handler
import android.os.HandlerThread
import android.view.FrameMetrics
import android.view.Window
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.platform.ComposeView
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.servercontrolpanel.data.terminal.TerminalTicketSource
import dev.servercontrolpanel.data.terminal.TerminalWebSocket
import dev.servercontrolpanel.data.terminal.TerminalWebSocketFactory
import dev.servercontrolpanel.data.terminal.TerminalWebSocketListener
import dev.servercontrolpanel.data.terminal.WsTicketResult
import dev.servercontrolpanel.feature.terminal.render.GlyphAtlas
import dev.servercontrolpanel.feature.terminal.render.TerminalCanvas
import dev.servercontrolpanel.feature.terminal.transport.TerminalSocketClient
import dev.servercontrolpanel.terminalengine.CellSnapshot
import dev.servercontrolpanel.terminalengine.TerminalEngine
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
 * Like [GridThroughputBenchmark], but drives the fixture through the live path
 * (`TerminalSocketClient.onBytes` into `TerminalEngine.write`), so regressions in
 * the transport-to-render seam show up too.
 *
 * A fake [TerminalWebSocketFactory] replays the fixture as binary messages, and
 * [Dispatchers.Unconfined] runs each delivery on the thread that fires it.
 * Delivery is at frame pace ([CHUNKS_PER_FRAME] WS frames per `postOnAnimation`),
 * which also matches how a server streams a burst of output.
 *
 * Instrumented only: frame metrics need a real attached `Window`.
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
                    // TOTAL_DURATION includes vsync waits (33 ms on a 30 Hz emulator);
                    // DRAW_DURATION isolates the drawing cost being measured.
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
     * Fake `TerminalWebSocket` delivering [CHUNKS_PER_FRAME] binary messages per
     * animation frame, yielding between batches so frames are drawn and measured.
     * It never closes itself.
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
        /** 60Hz frame budget; longer frames count as dropped. */
        const val FRAME_BUDGET_MILLIS = 16L
        const val SETTLE_MILLIS = 2_000L

        /** WS frames per animation frame; same as GridThroughputBenchmark so results compare. */
        const val CHUNKS_PER_FRAME = 8
    }
}
