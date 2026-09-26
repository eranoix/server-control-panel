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

/**
 * Captures real screenshots (engine, renderer and overlay) of each scroll state, since
 * a misplaced or missing indicator is not caught by assertions. PNGs are written to
 * the test package's external files directory for `adb pull`.
 */
@RunWith(AndroidJUnit4::class)
class ScrollCaptureTest {

    @get:Rule
    val rule = createAndroidComposeRule<ComponentActivity>()

    // Fills the emulator's 1080x2400 screen like the real grid, so the capture has
    // no black band that the app would not show.
    private val cols = 76
    private val lines = 79
    private val cellWidth = 14
    private val cellHeight = 30

    private fun outputDir(): File {
        val ctx = InstrumentationRegistry.getInstrumentation().targetContext
        val dir = File(ctx.getExternalFilesDir(null), "capturas-rolagem")
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
            // Each capture is about 10 MB; keeping several until GC crashes the test
            // process, so recycle immediately.
            bitmap.recycle()
        }
    }

    /** A long listing shaped like `ls -la /usr/bin`. */
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

    // Compose allows one setContent per test, so captures are driven by state.
    private val currentSnapshot = mutableStateOf<CellSnapshot?>(null)
    private val currentScroll = mutableStateOf(TerminalScrollState.AT_END)
    private val currentNewOutput = mutableStateOf(false)
    private val currentLightTheme = mutableStateOf(false)

    private fun buildOnce() {
        rule.setContent {
            val light = currentLightTheme.value
            val palette = if (light) LightTerminalPalette else DarkTerminalPalette
            // One atlas for the whole test: cell size does not depend on the theme, and
            // an atlas per theme would leak the previous one's bitmaps.
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
        // Let the overlay fade finish so the indicator is not captured translucent.
        rule.mainClock.advanceTimeBy(1_000L)
        rule.waitForIdle()
    }

    @Test
    fun capture_allScrollStates() {
        val engine = TerminalEngine.create(cols = cols, rows = lines)
        try {
            longListing(engine, 300)
            buildOnce()

            // 1. Pinned to the bottom: no indicator at all.
            show(engine)
            save("01-no-fim-sem-indicador.png")

            // 2. Mid-history: position bar and "Back to the end" with the distance.
            engine.scrollViewport(-120)
            show(engine)
            save("02-rolado-para-cima.png")

            // 3. Top of history: the bar touches the top.
            engine.scrollToTop()
            show(engine)
            save("03-no-topo-do-historico.png")

            // 4. New output while reading: the screen does not jump and the button
            //    announces it.
            engine.scrollViewport(60)
            engine.write("new-command-just-arrived\r\n".toByteArray(Charsets.UTF_8))
            show(engine, hasNewOutput = true)
            save("04-saida-nova-sem-saltar.png")

            // 5. Same state in the light theme.
            show(engine, light = true)
            save("05-tema-claro.png")
        } finally {
            engine.close()
        }
    }

    /**
     * The alternate screen (`htop`, `vim`) has no history to navigate, so no indicator
     * may appear; gestures go to the program instead.
     */
    @Test
    fun capture_altScreenShowsNoIndicator() {
        val engine = TerminalEngine.create(cols = cols, rows = lines)
        try {
            longListing(engine, 300)
            engine.write("\u001b[?1049h\u001b[H".toByteArray(Charsets.UTF_8))
            // Like `htop`, request the mouse so drags become wheel events.
            engine.write("\u001b[?1000h".toByteArray(Charsets.UTF_8))
            val sb = StringBuilder()
            sb.append("  PID USER      PRI  NI  VIRT   RES   SHR S CPU% MEM%\r\n")
            for (i in 1..14) {
                sb.append(String.format("%5d test       20   0  %5dM %4dM %4dM S %4.1f %4.1f\r\n",
                    1000 + i, 100 + i, 30 + i, 10 + i, (i * 3.1), (i * 0.7)))
            }
            engine.write(sb.toString().toByteArray(Charsets.UTF_8))

            // The library pins the viewport to the active area on the alternate screen.
            engine.scrollViewport(-100)

            buildOnce()
            show(engine)
            save("06-tela-alternativa-htop.png")

            assertTrue("on the alternate screen the viewport stays pinned", engine.scrollState().atEnd)
        } finally {
            engine.close()
        }
    }
}
