package com.vpsmanager.feature.terminal.input

import android.view.KeyEvent
import android.view.View
import com.vpsmanager.feature.terminal.prefs.ModoDeDigitacao
import com.vpsmanager.feature.terminal.selection.GridSelection
import com.vpsmanager.feature.terminal.selection.GridSelectionHolder
import com.vpsmanager.terminalengine.KeyByteEncoder
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

/**
 * Drives the `InputConnection` API directly — no IME needed — and asserts on
 * the exact bytes recorded by [RecordingByteSink]. This is the contract proof:
 * what a well-behaved caller of these exact method calls produces. It cannot
 * prove what a real Gboard or CJK IME actually calls through that contract;
 * that is the on-device checkpoint.
 */
@RunWith(RobolectricTestRunner::class)
class TerminalInputConnectionTest {

    private lateinit var sink: RecordingByteSink
    private lateinit var view: View
    private var fakeNowNanos: Long = 0L

    @Before
    fun setUp() {
        sink = RecordingByteSink()
        view = View(RuntimeEnvironment.getApplication())
    }

    private var composicaoVista: String = ""

    private fun newConnection(
        cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
        modo: ModoDeDigitacao = ModoDeDigitacao.PADRAO,
    ): TerminalInputConnection = TerminalInputConnection(
        view = view,
        sink = sink,
        cursorMode = cursorMode,
        modo = { modo },
        aoMudarComposicao = { texto -> composicaoVista = texto },
        nowNanos = { fakeNowNanos },
    )

    private fun keyDown(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, code, 0, metaState)

    private fun keyUp(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_UP, code, 0, metaState)

    @Test
    fun `commitText sends exactly the committed bytes once`() {
        val connection = newConnection()
        connection.commitText("ls", 1)
        assertEquals("6c 73", sink.hex())
    }

    @Test
    fun `no modo TEXTO a composicao fica retida ate ser confirmada`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ni", 1)
        connection.setComposingText("nih", 1)
        assertTrue("composing must not touch the sink", sink.isEmpty())

        connection.commitText("你好", 1)
        assertEquals("e4 bd a0 e5 a5 bd", sink.hex())
    }

    @Test
    fun `no modo TERMINAL a composicao NAO fica presa — cada pedaco novo sai na hora`() {
        // This is the defect that TERMINAL mode closes. With `TYPE_NULL` no
        // keyboard SHOULD compose, but the ones that ignore the type (Samsung)
        // did — and then the screen sat still while the fingers moved, because
        // nothing reached the terminal until the word was finished.
        val connection = newConnection(modo = ModoDeDigitacao.TERMINAL)
        connection.setComposingText("l", 1)
        assertEquals("6c", sink.hex())
        connection.setComposingText("ls", 1)
        assertEquals("6c 73", sink.hex())

        // Confirming what has already gone out must not send it again.
        connection.commitText("ls", 1)
        assertEquals("confirmar nao pode duplicar o que ja ecoou", "6c 73", sink.hex())
    }

    @Test
    fun `no modo TERMINAL encolher a composicao manda DEL`() {
        val connection = newConnection(modo = ModoDeDigitacao.TERMINAL)
        connection.setComposingText("ls", 1)
        assertEquals("6c 73", sink.hex())
        connection.setComposingText("l", 1)
        assertEquals("6c 73 7f", sink.hex())
    }

    @Test
    fun `no modo TERMINAL uma troca de palavra apaga o que ja tinha ecoado`() {
        // Autocorrect acting in a mode that never asked for autocorrect: we
        // already echoed "teh" and the keyboard confirms "the". Without the
        // erase, the screen would read "tehthe".
        val connection = newConnection(modo = ModoDeDigitacao.TERMINAL)
        connection.setComposingText("teh", 1)
        assertEquals("74 65 68", sink.hex())

        connection.commitText("the", 1)
        assertEquals("74 65 68 7f 7f 7f 74 68 65", sink.hex())
    }

    @Test
    fun `finishComposingText entrega a palavra em voo em vez de perde-la`() {
        // This call used to DISCARD the composition: switching apps or
        // tapping away mid-word was enough to make it vanish without ever
        // reaching the terminal. `finishComposingText` means "take it as it
        // stands", not "throw it away" — Termux also flushes here.
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("hel", 1)
        assertTrue("nada sai enquanto compoe", sink.isEmpty())

        connection.finishComposingText()
        assertEquals("68 65 6c", sink.hex())
        assertEquals("", connection.composingTextForTest())
    }

    @Test
    fun `uma tecla de comando entrega a composicao ANTES de si`() {
        // Enter with a word in flight: the word has to arrive before the
        // 0d, otherwise it goes out after the Enter meant to send it.
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ls", 1)
        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_ENTER))
        assertEquals("6c 73 0d", sink.hex())
    }

    @Test
    fun `a faixa de composicao e avisada a cada mudanca e no fim`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("te", 1)
        assertEquals("te", composicaoVista)
        connection.setComposingText("tes", 1)
        assertEquals("tes", composicaoVista)

        connection.commitText("teste", 1)
        assertEquals("confirmada a palavra, a faixa some", "", composicaoVista)
    }

    @Test
    fun `an IME-issued key event matching a just-committed word is dropped, not duplicated`() {
        val connection = newConnection()
        connection.commitText("word", 1)
        val afterCommit = sink.hex()

        // Some IMEs synthesize a matching sendKeyEvent for characters they
        // just committed. 'w' is next in the dedup queue and arrives well
        // inside the dedup window.
        fakeNowNanos += 10_000_000L // +10ms, still inside the 150ms window
        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_W))
        connection.sendKeyEvent(keyUp(KeyEvent.KEYCODE_W))

        assertEquals("bytes must appear once, not twice", afterCommit, sink.hex())
    }

    @Test
    fun `deleteSurroundingText with no composing region sends one DEL byte`() {
        val connection = newConnection()
        connection.deleteSurroundingText(1, 0)
        assertEquals("7f", sink.hex())
    }

    @Test
    fun `deleteSurroundingText during composition shrinks the buffer locally with zero bytes`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ab", 1)
        connection.deleteSurroundingText(1, 0)
        assertTrue("no bytes must reach the sink mid-composition", sink.isEmpty())
        assertEquals("a", connection.composingTextForTest())

        connection.commitText(connection.composingTextForTest(), 1)
        assertEquals("61", sink.hex())
    }

    @Test
    fun `hardware key events encode arrows, ctrl, alt, tab, esc and enter`() {
        val connection = newConnection()

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_DPAD_UP))
        assertEquals("1b 5b 41", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_C, KeyEvent.META_CTRL_ON))
        assertEquals("1b 5b 41 03", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_F, KeyEvent.META_ALT_ON))
        assertEquals("1b 5b 41 03 1b 66", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_TAB))
        assertEquals("1b 5b 41 03 1b 66 09", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_ESCAPE))
        assertEquals("1b 5b 41 03 1b 66 09 1b", sink.hex())

        connection.sendKeyEvent(keyDown(KeyEvent.KEYCODE_ENTER))
        assertEquals("1b 5b 41 03 1b 66 09 1b 0d", sink.hex())
    }

    @Test
    fun `key-up events never emit bytes on their own`() {
        val connection = newConnection()
        connection.sendKeyEvent(keyUp(KeyEvent.KEYCODE_ENTER))
        assertTrue(sink.isEmpty())
    }

    @Test
    fun `o conteudo do terminal nunca e exposto ao IME — em modo nenhum`() {
        // The GUARANTEE is about privacy, and it has not changed: a keyboard
        // is a third-party app, and what the grid shows — command output, a
        // token, an echoed password — can never reach it.
        //
        // What changed was the WAY of asserting it. The test used to demand an
        // empty string, confusing the guarantee with its implementation; and
        // that emptiness was precisely what made the Samsung keyboard swallow
        // the arrow key. Now the test asserts the guarantee directly: what
        // leaves here contains nothing from the terminal. The virtual-context
        // sentinels are fixed, known constants with no information inside.
        for (modo in ModoDeDigitacao.entries) {
            sink.clear()
            val connection = newConnection(modo = modo)
            connection.commitText("hello", 1) // went to the terminal, not to the IME

            val exposto = connection.getTextBeforeCursor(10, 0).toString() +
                connection.getTextAfterCursor(10, 0).toString() +
                (connection.getExtractedText(null, 0)?.text?.toString() ?: "")

            assertTrue(
                "conteudo do terminal vazou para o IME em $modo: $exposto",
                !exposto.contains("hello"),
            )
            // Nothing beyond the sentinels: with no composition in flight,
            // not a single letter is left in what the keyboard sees.
            assertEquals(
                "so as sentinelas podiam estar ali em $modo",
                "",
                exposto.filter { it.isLetterOrDigit() },
            )
            assertNull("selecao e da grade, nao do IME", connection.getSelectedText(0))
        }
    }

    @Test
    fun `no modo TEXTO o corretor enxerga a composicao — era isso que faltava`() {
        // The root cause of the wrong corrections: the app declared a text
        // field and returned "" here, so autocorrect decided the replacement
        // against emptiness.
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("comec", 1)

        // The composition is there, at the end of what precedes the cursor.
        // What comes before it is the virtual-context sentinel, which is not a
        // letter and so does not join the word autocorrect extracts.
        assertTrue(connection.getTextBeforeCursor(10, 0).toString().endsWith("comec"))
        assertEquals("ec", connection.getTextBeforeCursor(2, 0).toString())

        val extraido = connection.getExtractedText(null, 0)!!
        assertTrue(
            "a composicao tem que aparecer no texto extraido",
            extraido.text.toString().contains("comec"),
        )
        // The cursor sits AFTER the composition and BEFORE the right sentinel.
        assertEquals("comec", extraido.text.toString().substring(1, extraido.selectionStart))
    }

    @Test
    fun `no modo TERMINAL o teclado nao recebe TEXTO — mas recebe cursor`() {
        // This test CHANGED ITS INTENT on purpose, and the name changed with it.
        //
        // Before: "receives no context at all". That made sense while we
        // believed `TYPE_NULL` was enough. It is not — Samsung keyboards
        // ignore `TYPE_NULL`, and it is in TERMINAL mode that the arrow key is
        // indispensable. ZERO context was what made Honeyboard conclude
        // "cursor at the boundary" and swallow the key.
        //
        // Now: the keyboard receives no TEXT (neither from the terminal nor
        // from the composition — the composition here already went straight to
        // the terminal), but it does receive a cursor that is not at a boundary.
        val connection = newConnection(modo = ModoDeDigitacao.TERMINAL)
        connection.setComposingText("comec", 1)

        val antes = connection.getTextBeforeCursor(10, 0).toString()
        assertEquals("nenhuma letra pode aparecer aqui", "", antes.filter { it.isLetterOrDigit() })
        assertTrue("mas nao pode estar vazio — e o que engolia a seta", antes.isNotEmpty())
        assertTrue(connection.getTextAfterCursor(10, 0).toString().isNotEmpty())
    }

    @Test
    fun `grid selection and ime composition state do not observe each other`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        val selectionHolder = GridSelectionHolder()

        // Direction 1: composition in progress, then a selection is set.
        connection.setComposingText("ni", 1)
        selectionHolder.selection = GridSelection(startRow = 2, startCol = 3, endRow = 2, endCol = 7)
        assertEquals("selection must not touch composing state", "ni", connection.composingTextForTest())

        connection.setComposingText("nih", 1)
        connection.commitText("你好", 1)
        assertEquals(
            "composition committed unaffected by the selection change",
            "e4 bd a0 e5 a5 bd",
            sink.hex(),
        )

        // Direction 2: a selection exists, then composition starts and commits.
        val expectedSelection = GridSelection(startRow = 0, startCol = 0, endRow = 0, endCol = 4)
        selectionHolder.selection = expectedSelection
        connection.setComposingText("h", 1)
        // Flushes the "h" to the terminal (see the finishComposingText test);
        // what is under proof here is that the SELECTION did not move with it.
        connection.finishComposingText()
        assertEquals(
            "composition must not touch the selection",
            expectedSelection,
            selectionHolder.selection,
        )
    }
    // --- virtual context (Samsung / Honeyboard keyboard) ---------------------
    //
    // Honeyboard checks the cursor boundary BEFORE emitting the key: seeing
    // emptiness on both sides, it concludes the cursor is at the boundary and
    // swallows the arrow. The tests below lock the contract that prevents
    // this, and above all lock what MUST NOT happen: no sentinel may ever
    // become a byte. See https://github.com/termux/termux-app/pull/5287

    @Test
    fun `nenhum dos lados do cursor fica vazio — e o que engolia a seta`() {
        for (modo in ModoDeDigitacao.entries) {
            val connection = newConnection(modo = modo)
            assertTrue(
                "antes do cursor nao pode ser vazio em $modo",
                connection.getTextBeforeCursor(20, 0)!!.isNotEmpty(),
            )
            assertTrue(
                "depois do cursor nao pode ser vazio em $modo",
                connection.getTextAfterCursor(20, 0)!!.isNotEmpty(),
            )
        }
    }

    @Test
    fun `a sentinela NUNCA vira byte no terminal`() {
        // The guarantee that holds the whole solution up: the virtual context
        // exists only as IME metadata. If a Private Use character showed up in
        // the output, it would end up on the grid and in the PTY.
        for (modo in ModoDeDigitacao.entries) {
            sink.clear()
            val connection = newConnection(modo = modo)
            connection.getTextBeforeCursor(20, 0)
            connection.getTextAfterCursor(20, 0)
            connection.getExtractedText(null, 0)
            connection.setComposingText("ola", 1)
            connection.finishComposingText()
            connection.commitText("ls", 1)
            connection.deleteSurroundingText(1, 0)

            val saida = String(sink.bytes(), Charsets.UTF_8)
            assertTrue(
                "sentinela esquerda vazou para o terminal em $modo: ${sink.hex()}",
                !saida.contains('\uE000'),
            )
            assertTrue(
                "sentinela direita vazou para o terminal em $modo: ${sink.hex()}",
                !saida.contains('\uE001'),
            )
        }
    }

    @Test
    fun `a palavra real continua legivel para o corretor, sem a sentinela colada`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ola", 1)
        val antes = connection.getTextBeforeCursor(20, 0).toString()

        // Autocorrect looks for the WORD. The sentinel is from the Private
        // Use Area — not a letter — so segmentation stops at it instead of
        // swallowing it.
        assertTrue("a composicao real tem que estar ali: $antes", antes.endsWith("ola"))
        val ultimaPalavra = antes.takeLastWhile { it.isLetter() }
        assertEquals("ola", ultimaPalavra)
    }

    @Test
    fun `o texto extraido conta a MESMA historia dos getters — cursor no meio`() {
        // An IME that got a cursor at the end here and a cursor in the middle
        // there would go back to deciding by boundary. The three answers have
        // to agree.
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ola", 1)

        val extraido = connection.getExtractedText(null, 0)!!
        val texto = extraido.text.toString()
        assertTrue("tem que sobrar texto depois do cursor", extraido.selectionStart < texto.length)
        assertTrue("tem que existir texto antes do cursor", extraido.selectionStart > 0)
        assertEquals(extraido.selectionStart, extraido.selectionEnd)
        assertEquals("ola", texto.substring(1, extraido.selectionStart))
    }

    @Test
    fun `o corte por n respeita o pedido do teclado`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("comando", 1)
        assertEquals(3, connection.getTextBeforeCursor(3, 0)!!.length)
        assertEquals("", connection.getTextBeforeCursor(0, 0)!!.toString())
        assertEquals("", connection.getTextAfterCursor(0, 0)!!.toString())
    }

    @Test
    fun `a troca de palavra do corretor nao come a sentinela`() {
        // Autocorrect asks to delete the word and commits the new one. Had it
        // counted the sentinel in, it would delete one character too many —
        // and in a terminal that is a DEL nobody asked for.
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ola", 1)
        sink.clear()
        connection.deleteSurroundingText(3, 0) // exactly the word
        assertTrue("nada devia ir ao terminal: a palavra so existia no IME", sink.isEmpty())
        assertEquals("", connection.composingTextForTest())
    }

    @Test
    fun `um pedido de apagar maior que a composicao nao inventa DEL extra`() {
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        connection.setComposingText("ola", 1)
        sink.clear()
        connection.deleteSurroundingText(99, 0)
        assertTrue("a composicao e local; nada vai ao terminal", sink.isEmpty())
    }

    @Test
    fun `lote de edicao e aceito e respeita o aninhamento`() {
        // Answering `false` means "I cannot do batches", and an IME that
        // hears that may give up on the word replacement.
        val connection = newConnection(modo = ModoDeDigitacao.TEXTO)
        assertTrue(connection.beginBatchEdit())
        assertTrue("lote interno ainda aberto", connection.beginBatchEdit())
        assertTrue("ainda resta um lote", connection.endBatchEdit())
        assertEquals(false, connection.endBatchEdit())
        assertEquals(false, connection.endBatchEdit()) // never goes negative
    }

    @Test
    fun `capitalizacao automatica responde no modo TEXTO e cala no TERMINAL`() {
        // TerminalInputView declares CAP_SENTENCES in TEXT mode; without this
        // the promise was never kept.
        val texto = newConnection(modo = ModoDeDigitacao.TEXTO)
        assertTrue(
            "inicio de frase tem que pedir maiuscula",
            texto.getCursorCapsMode(android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES) != 0,
        )
        val terminal = newConnection(modo = ModoDeDigitacao.TERMINAL)
        assertEquals(
            0,
            terminal.getCursorCapsMode(android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES),
        )
    }

}
