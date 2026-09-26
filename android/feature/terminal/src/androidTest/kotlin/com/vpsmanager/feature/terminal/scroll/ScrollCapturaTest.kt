package com.vpsmanager.feature.terminal.scroll

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
import com.vpsmanager.feature.terminal.render.GlyphAtlas
import com.vpsmanager.feature.terminal.render.PaletaTerminalClara
import com.vpsmanager.feature.terminal.render.PaletaTerminalEscura
import com.vpsmanager.feature.terminal.render.TerminalCanvas
import com.vpsmanager.terminalengine.CellSnapshot
import com.vpsmanager.terminalengine.TerminalEngine
import com.vpsmanager.terminalengine.TerminalScrollState
import java.io.File
import java.io.FileOutputStream
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Captures the screen FOR REAL — real engine, real renderer, real overlay — in
 * each state the owner needs to recognise at a glance.
 *
 * It exists because visual proof is not decoration: an indicator that appears
 * in the wrong place, or disappears when it should stay, is a defect no
 * assertion test catches. The PNGs land in the test package's files directory,
 * ready for `adb pull`.
 */
@RunWith(AndroidJUnit4::class)
class ScrollCapturaTest {

    @get:Rule
    val rule = createAndroidComposeRule<ComponentActivity>()

    // Geometry chosen to FILL the emulator's screen (1080x2400), as the real
    // grid does: TerminalRoute computes rows and columns from the measured
    // size. A grid smaller than the screen would leave the capture with a
    // black band that does not exist in the app.
    private val cols = 76
    private val linhas = 79
    private val larguraCelula = 14
    private val alturaCelula = 30

    private fun pasta(): File {
        val ctx = InstrumentationRegistry.getInstrumentation().targetContext
        val dir = File(ctx.getExternalFilesDir(null), "capturas-rolagem")
        dir.mkdirs()
        return dir
    }

    private fun salvar(nome: String) {
        val bitmap: Bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
        try {
            val arquivo = File(pasta(), nome)
            FileOutputStream(arquivo).use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            assertTrue("captura vazia: $nome", arquivo.length() > 0)
        } finally {
            // Each capture of this screen is about 10 MB (1080x2400 ARGB).
            // Holding six of them until the GC decided took down the whole
            // test PROCESS — the suite aborted in the FOLLOWING class, with
            // "Process crashed" and no assertion to explain it. Returning the
            // pixels immediately is the fix; holding onto screen bitmaps is
            // not optional.
            bitmap.recycle()
        }
    }

    /** A long listing, in the shape of an `ls -la /usr/bin`. */
    private fun listagemLonga(engine: TerminalEngine, quantidade: Int) {
        val nomes = listOf(
            "apt", "awk", "base64", "bash", "cat", "chmod", "curl", "dash", "date",
            "dd", "df", "diff", "dmesg", "du", "env", "find", "grep", "gzip", "head",
            "id", "join", "kill", "less", "ln", "ls", "make", "mkdir", "mv", "nano",
            "nc", "nl", "od", "ping", "ps", "python3", "rm", "sed", "sort", "ssh",
            "tail", "tar", "tee", "top", "tr", "uniq", "vim", "wc", "wget", "zip",
        )
        val sb = StringBuilder()
        sb.append("teste@vpsm:~$ ls -la /usr/bin\r\n")
        sb.append("total ").append(quantidade * 4).append("\r\n")
        for (i in 0 until quantidade) {
            val nome = nomes[i % nomes.size]
            sb.append("-rwxr-xr-x 1 root root ")
            sb.append(String.format("%7d", 10240 + i * 37))
            sb.append(" set  6 19:0")
            sb.append(i % 10)
            sb.append(' ')
            sb.append(nome)
            sb.append('-')
            sb.append(i)
            sb.append("\r\n")
        }
        sb.append("teste@vpsm:~$ ")
        engine.write(sb.toString().toByteArray(Charsets.UTF_8))
    }

    // A single setContent per test (Compose allows only one), with the
    // content driven by state: each capture swaps the state and waits for the
    // frame.
    private val snapshotAtual = mutableStateOf<CellSnapshot?>(null)
    private val rolagemAtual = mutableStateOf(TerminalScrollState.NO_FIM)
    private val saidaNovaAtual = mutableStateOf(false)
    private val temaClaroAtual = mutableStateOf(false)

    private fun montarUmaVez() {
        rule.setContent {
            val claro = temaClaroAtual.value
            val paleta = if (claro) PaletaTerminalClara else PaletaTerminalEscura
            // One atlas for the life of the test: the cell size does not
            // depend on the theme, and one atlas per theme left the previous
            // one's bitmaps with no owner.
            val atlas = remember { GlyphAtlas(cellWidthPx = larguraCelula, cellHeightPx = alturaCelula) }
            MaterialTheme(colorScheme = if (claro) lightColorScheme() else darkColorScheme()) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(Color(paleta.defaultBg)),
                ) {
                    TerminalCanvas(
                        snapshotState = snapshotAtual,
                        cellWidthPx = larguraCelula.toFloat(),
                        cellHeightPx = alturaCelula.toFloat(),
                        glyphAtlas = atlas,
                        modifier = Modifier.fillMaxSize(),
                        palette = paleta,
                    )
                    ScrollPositionOverlay(
                        estado = rolagemAtual.value,
                        haSaidaNova = saidaNovaAtual.value,
                        aoVoltarAoFim = {},
                    )
                }
            }
        }
        rule.waitForIdle()
    }

    private fun mostrar(
        engine: TerminalEngine,
        haSaidaNova: Boolean = false,
        claro: Boolean = false,
    ) {
        rule.runOnUiThread {
            snapshotAtual.value = engine.snapshot()
            rolagemAtual.value = engine.scrollState()
            saidaNovaAtual.value = haSaidaNova
            temaClaroAtual.value = claro
        }
        rule.waitForIdle()
        // The overlay fades in and out: without advancing the clock, the
        // capture catches the animation mid-way and the indicator comes out
        // translucent.
        rule.mainClock.advanceTimeBy(1_000L)
        rule.waitForIdle()
    }

    @Test
    fun captura_todosOsEstadosDaRolagem() {
        val engine = TerminalEngine.create(cols = cols, rows = linhas)
        try {
            listagemLonga(engine, 300)
            montarUmaVez()

            // 1. Pinned to the bottom: the grid stays CLEAN, with no
            //    indicator at all.
            mostrar(engine)
            salvar("01-no-fim-sem-indicador.png")

            // 2. Scrolled into the middle of the history: the position bar on
            //    the right and "Back to the end" with the distance travelled.
            engine.scrollViewport(-120)
            mostrar(engine)
            salvar("02-rolado-para-cima.png")

            // 3. At the top of the history: the bar touches the top.
            engine.scrollToTop()
            mostrar(engine)
            salvar("03-no-topo-do-historico.png")

            // 4. New output arrived while reading: the screen did NOT jump,
            //    and the button starts announcing the news.
            engine.scrollViewport(60)
            engine.write("comando-novo-chegou-agora\r\n".toByteArray(Charsets.UTF_8))
            mostrar(engine, haSaidaNova = true)
            salvar("04-saida-nova-sem-saltar.png")

            // 5. The same state in the light theme — the overlay follows the
            //    theme.
            mostrar(engine, claro = true)
            salvar("05-tema-claro.png")
        } finally {
            engine.close()
        }
    }

    /**
     * The alternate screen (`htop`, `vim`): there is no history to navigate, so
     * NO indicator may appear — neither bar nor button. The gesture there
     * becomes a wheel or an arrow for the program, not a local scroll.
     */
    @Test
    fun captura_telaAlternativaNaoMostraIndicador() {
        val engine = TerminalEngine.create(cols = cols, rows = linhas)
        try {
            listagemLonga(engine, 300)
            engine.write("\u001b[?1049h\u001b[H".toByteArray(Charsets.UTF_8))
            // `htop` asks for the mouse: with that, a vertical drag becomes a
            // WHEEL for it rather than a local scroll — and that is why there
            // is nothing to indicate.
            engine.write("\u001b[?1000h".toByteArray(Charsets.UTF_8))
            val sb = StringBuilder()
            sb.append("  PID USER      PRI  NI  VIRT   RES   SHR S CPU% MEM%\r\n")
            for (i in 1..14) {
                sb.append(String.format("%5d teste      20   0  %5dM %4dM %4dM S %4.1f %4.1f\r\n",
                    1000 + i, 100 + i, 30 + i, 10 + i, (i * 3.1), (i * 0.7)))
            }
            engine.write(sb.toString().toByteArray(Charsets.UTF_8))

            // Try to scroll: the library pins the viewport to the active area.
            engine.scrollViewport(-100)

            montarUmaVez()
            mostrar(engine)
            salvar("06-tela-alternativa-htop.png")

            assertTrue("na tela alternativa o viewport fica preso", engine.scrollState().noFim)
        } finally {
            engine.close()
        }
    }
}
