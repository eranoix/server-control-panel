package dev.servercontrolpanel.feature.terminal.scroll

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.servercontrolpanel.feature.terminal.render.GlyphAtlas
import dev.servercontrolpanel.feature.terminal.render.LightTerminalPalette
import dev.servercontrolpanel.feature.terminal.render.DarkTerminalPalette
import dev.servercontrolpanel.feature.terminal.render.TerminalCanvas
import dev.servercontrolpanel.terminalengine.CellSnapshot
import dev.servercontrolpanel.terminalengine.TerminalEngine
import dev.servercontrolpanel.terminalengine.TerminalScrollState
import java.io.File
import java.io.FileOutputStream
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class ScrollCaptureTest {

    @get:Rule
    val rule = createAndroidComposeRule<ComponentActivity>()

    private val cols = 76
    private val lines = 79
    private val cellWidth = 14
    private val cellHeight = 30

    private fun outputDir(): File {
        val ctx = InstrumentationRegistry.getInstrumentation().targetContext
        val dir = File(ctx.getExternalFilesDir(null), "scroll-captures")
        dir.mkdirs()
        return dir
    }

    private fun save(name: String) {
        val bitmap: Bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
        try {
            val file = File(outputDir(), name)
            FileOutputStream(file).use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            assertTrue("empty capture: $name", file.length() > 0)
        } finally {
            bitmap.recycle()
        }
    }

    private fun longListing(engine: TerminalEngine, count: Int) {
        val names = listOf(
            "apt", "awk", "base64", "bash", "cat", "chmod", "curl", "dash", "date",
            "dd", "df", "diff", "dmesg", "du", "env", "find", "grep", "gzip", "head",
            "id", "join", "kill", "less", "ln", "ls", "make", "mkdir", "mv", "nano",
            "nc", "nl", "od", "ping", "ps", "python3", "rm", "sed", "sort", "ssh",
            "tail", "tar", "tee", "top", "tr", "uniq", "vim", "wc", "wget", "zip",
        )
        val sb = StringBuilder()
        sb.append("test@panel:~$ ls -la /usr/bin\r\n")
        sb.append("total ").append(count * 4).append("\r\n")
        for (i in 0 until count) {
            val name = names[i % names.size]
            sb.append("-rwxr-xr-x 1 root root ")
            sb.append(String.format("%7d", 10240 + i * 37))
            sb.append(" Sep  6 19:0")
            sb.append(i % 10)
            sb.append(' ')
            sb.append(name)
            sb.append('-')
            sb.append(i)
            sb.append("\r\n")
        }
        sb.append("test@panel:~$ ")
        engine.write(sb.toString().toByteArray(Charsets.UTF_8))
    }

    private val currentSnapshot = mutableStateOf<CellSnapshot?>(null)
    private val currentScroll = mutableStateOf(TerminalScrollState.AT_END)
    private val currentNewOutput = mutableStateOf(false)
    private val currentLightTheme = mutableStateOf(false)

    private fun buildOnce() {
        rule.setContent {
            val light = currentLightTheme.value
            val palette = if (light) LightTerminalPalette else DarkTerminalPalette
            val atlas = remember { GlyphAtlas(cellWidthPx = cellWidth, cellHeightPx = cellHeight) }
            MaterialTheme(colorScheme = if (light) lightColorScheme() else darkColorScheme()) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(Color(palette.defaultBg)),
                ) {
                    TerminalCanvas(
                        snapshotState = currentSnapshot,
                        cellWidthPx = cellWidth.toFloat(),
                        cellHeightPx = cellHeight.toFloat(),
                        glyphAtlas = atlas,
                        modifier = Modifier.fillMaxSize(),
                        palette = palette,
                    )
                    ScrollPositionOverlay(
                        state = currentScroll.value,
                        hasNewOutput = currentNewOutput.value,
                        onBackToEnd = {},
                    )
                }
            }
        }
        rule.waitForIdle()
    }

    private fun show(
        engine: TerminalEngine,
        hasNewOutput: Boolean = false,
        light: Boolean = false,
    ) {
        rule.runOnUiThread {
            currentSnapshot.value = engine.snapshot()
            currentScroll.value = engine.scrollState()
            currentNewOutput.value = hasNewOutput
            currentLightTheme.value = light
        }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000L)
        rule.waitForIdle()
    }

    @Test
    fun capture_allScrollStates() {
        val engine = TerminalEngine.create(cols = cols, rows = lines)
        try {
            longListing(engine, 300)
            buildOnce()

            show(engine)
            save("01-at-end-no-indicator.png")

            engine.scrollViewport(-120)
            show(engine)
            save("02-scrolled-up.png")

            engine.scrollToTop()
            show(engine)
            save("03-at-top-of-history.png")

            engine.scrollViewport(60)
            engine.write("new-command-just-arrived\r\n".toByteArray(Charsets.UTF_8))
            show(engine, hasNewOutput = true)
            save("04-new-output-no-jump.png")

            show(engine, light = true)
            save("05-light-theme.png")
        } finally {
            engine.close()
        }
    }

    @Test
    fun capture_altScreenShowsNoIndicator() {
        val engine = TerminalEngine.create(cols = cols, rows = lines)
        try {
            longListing(engine, 300)
            engine.write("\u001b[?1049h\u001b[H".toByteArray(Charsets.UTF_8))
            engine.write("\u001b[?1000h".toByteArray(Charsets.UTF_8))
            val sb = StringBuilder()
            sb.append("  PID USER      PRI  NI  VIRT   RES   SHR S CPU% MEM%\r\n")
            for (i in 1..14) {
                sb.append(String.format("%5d test       20   0  %5dM %4dM %4dM S %4.1f %4.1f\r\n",
                    1000 + i, 100 + i, 30 + i, 10 + i, (i * 3.1), (i * 0.7)))
            }
            engine.write(sb.toString().toByteArray(Charsets.UTF_8))

            engine.scrollViewport(-100)

            buildOnce()
            show(engine)
            save("06-alternate-screen-htop.png")

            assertTrue("on the alternate screen the viewport stays pinned", engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }
}
