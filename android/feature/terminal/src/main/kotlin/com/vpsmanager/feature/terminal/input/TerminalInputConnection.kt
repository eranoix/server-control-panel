package com.vpsmanager.feature.terminal.input

import android.text.Editable
import android.text.TextUtils
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.ExtractedText
import android.view.inputmethod.ExtractedTextRequest
import com.vpsmanager.feature.terminal.prefs.ModoDeDigitacao
import com.vpsmanager.terminalengine.KeyByteEncoder

/**
 * The IME's single door into the terminal.
 *
 * ## What was wrong
 *
 * This class declared an ordinary text field to the keyboard
 * (`TYPE_CLASS_TEXT or TYPE_TEXT_FLAG_MULTI_LINE`, in [TerminalInputView]) and,
 * at the same time, answered every getter that **there is no text here**:
 * `getTextBeforeCursor` returned `""`, `getExtractedText` returned `null`,
 * `getSelectedText` returned `null`. Both cannot be true.
 *
 * Autocorrect decides its substitution from the text around the cursor.
 * Receiving `""`, it corrected against emptiness — hence the wrong
 * corrections. And keyboards that notice the editor "did not keep" what was
 * committed resend the word as synthetic key events; that is why a 150 ms
 * CLOCK-based tie-break used to live here. That tie-break was the symptom of a
 * contradiction, not the cure for it.
 *
 * ## How it ended up
 *
 * The behaviour is now defined by [ModoDeDigitacao], read ON EVERY call (never
 * from a stored copy — the person switches mode mid-session, and a copy would
 * go stale in silence, for the same reason documented in `RoteamentoDeToque`):
 *
 * - [ModoDeDigitacao.TERMINAL] — [TerminalInputView] declares `TYPE_NULL` and
 *   the keyboard sends key events. If a keyboard ignores `TYPE_NULL` and
 *   composes anyway (Samsung is the known case), the text **is not held back**:
 *   each new piece of the composition goes straight to the terminal, and
 *   shrinking the composition sends DEL. The terminal is never stuck while you
 *   type.
 * - [ModoDeDigitacao.TEXTO] — the composition is kept AND returned by the
 *   getters, which is the contract the `inputType` promises. Autocorrect gets
 *   to see what has already been typed; the word only goes to the terminal once
 *   confirmed.
 *
 * No `Editable` is materialized in either mode: the constructor stays
 * `fullEditor = false`. An `Editable` would be a second owner of the content,
 * competing with the terminal's grid — the duality that sank the previous
 * attempt. What is kept here is only the composition in flight, which belongs
 * to the keyboard and to nobody else.
 */
class TerminalInputConnection(
    view: View,
    private val sink: ByteSink,
    private val cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    private val modo: () -> ModoDeDigitacao = { ModoDeDigitacao.PADRAO },
    private val aoMudarComposicao: (String) -> Unit = {},
    private val nowNanos: () -> Long = System::nanoTime,
) : BaseInputConnection(view, false) {

    /** What the keyboard believes it is composing right now. */
    private val composing = StringBuilder()

    /**
     * How many characters from the start of [composing] have ALREADY been
     * delivered to the terminal. Only goes above zero in TERMINAL mode, where
     * the composition is passed straight through instead of being held. It is
     * what lets a word be confirmed without resending what already went out.
     */
    private var jaEnviado = 0

    // --- IME vs. synthetic key tie-breaker -----------------------------------
    // Several keyboards call commitText(...) for the word AND, right after,
    // synthesise sendKeyEvent(...) for the same characters. With no
    // tie-breaker, every such word would come out duplicated. We remember what
    // has just been sent and, for a short window, swallow the key event whose
    // character is the next one expected. 150 ms sits well above a synthetic
    // resend within the same UI frame (sub-16 ms in practice) and well below
    // the fastest deliberate human typing, so it never eats a real keystroke.
    // With the IME contract fixed this becomes a safety net for non-standard
    // keyboards, and no longer the central patch.
    private val dedupQueue = ArrayDeque<Char>()
    private var dedupDeadlineNanos = Long.MIN_VALUE

    override fun getEditable(): Editable? = null

    override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
        val novo = text.toString()
        if (modo().componeTexto) {
            trocarComposicao(novo)
            return true
        }

        // TERMINAL mode: nothing may be held hostage by the keyboard. Pass the
        // difference through to the terminal now — the immediate echo a shell
        // demands.
        val prefixoComum = prefixoComum(composing, novo)
        if (jaEnviado > prefixoComum) {
            enviar(ByteArray(jaEnviado - prefixoComum) { DEL_BYTE })
            jaEnviado = prefixoComum
        }
        if (novo.length > jaEnviado) {
            enviarTexto(novo.substring(jaEnviado))
            jaEnviado = novo.length
        }
        trocarComposicao(novo)
        return true
    }

    override fun setComposingRegion(start: Int, end: Int): Boolean {
        // There is no Editable to carve a region out of; the composition is
        // only ever set whole, via setComposingText.
        return true
    }

    override fun finishComposingText(): Boolean {
        // The keyboard is saying "the composition stands as it is". Discarding
        // here LOST the word — switching apps or tapping outside mid-word was
        // enough. Whatever has not gone out yet goes out now.
        despejarPendente()
        limparComposicao()
        return true
    }

    override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
        val committed = text.toString()

        if (jaEnviado > 0) {
            // TERMINAL mode with a keyboard that composes anyway: part of this
            // is already on screen. If what is being confirmed starts with what
            // already went out, send only the remainder; if the keyboard
            // REPLACED the word (autocorrect acting in a mode that never asked
            // for it), erase what went out before sending the new version,
            // rather than leaving both on screen.
            if (committed.startsWith(composing.substring(0, jaEnviado))) {
                if (committed.length > jaEnviado) enviarTexto(committed.substring(jaEnviado))
            } else {
                enviar(ByteArray(jaEnviado) { DEL_BYTE })
                enviarTexto(committed)
            }
        } else if (committed.isNotEmpty()) {
            enviarTexto(committed)
        }

        limparComposicao()
        return true
    }

    override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
        if (composing.isNotEmpty() && jaEnviado == 0) {
            // Deleting inside the composition, TEXTO mode: shrink the local
            // buffer. Nothing was sent to the terminal for it, so there is
            // nothing to undo over there.
            val remove = minOf(beforeLength, composing.length)
            trocarComposicao(composing.substring(0, composing.length - remove))
            return true
        }
        // No pending composition (or nothing beyond what was already echoed in
        // TERMINAL mode): the delete belongs to the terminal. A Samsung
        // keyboard with "fix spelling" will ask for beforeLength > 1 at once.
        val remove = if (composing.isEmpty()) beforeLength else minOf(beforeLength, jaEnviado)
        if (remove > 0) enviar(ByteArray(remove) { DEL_BYTE })
        if (composing.isNotEmpty()) {
            jaEnviado -= remove
            trocarComposicao(composing.substring(0, composing.length - remove))
        }
        return true
    }

    override fun deleteSurroundingTextInCodePoints(beforeLength: Int, afterLength: Int): Boolean =
        deleteSurroundingText(beforeLength, afterLength)

    override fun sendKeyEvent(event: KeyEvent): Boolean {
        if (event.action != KeyEvent.ACTION_DOWN) return true

        val unicodeChar = event.unicodeChar
        if (unicodeChar != 0 && consumeIfDuplicate(unicodeChar.toChar())) {
            return true
        }

        val bytes = KeyByteEncoder.encode(event, cursorMode) ?: return false
        // A command key (Enter, arrows, Ctrl-something) ends the composition:
        // whatever is in flight has to reach the terminal BEFORE it, otherwise
        // the word comes out after the Enter that was meant to send it.
        if (composing.isNotEmpty()) {
            despejarPendente()
            limparComposicao()
        }
        sink.send(bytes)
        return true
    }

    override fun performEditorAction(actionCode: Int): Boolean {
        // The keyboard's Go/Done/Next action means nothing in a terminal;
        // Enter itself arrives as a key event through sendKeyEvent.
        return true
    }

    // --- what the IME sees ---------------------------------------------------
    //
    // These getters exist for two DIFFERENT reasons, and conflating them was
    // expensive.
    //
    // 1) AUTOCORRECT decides a replacement from the text surrounding the
    //    cursor. In TEXTO mode it is the real composition that has to show up
    //    here; returning "" was what made it correct against emptiness.
    //
    // 2) THE SAMSUNG KEYBOARD (`com.samsung.android.honeyboard`) runs its OWN
    //    cursor-boundary checks BEFORE emitting an event. It asks what lies
    //    before and after the cursor and, getting empty on both sides,
    //    concludes the cursor is at the end (or the start) of the text and
    //    **swallows the key** — the arrow never reaches us. On the device the
    //    symptom is an arrow key that simply does nothing, with no error at all.
    //
    // (2) is not a hypothesis of ours: it is documented in Termux, which is the
    // same use case (a terminal with its own InputConnection) —
    // <https://github.com/termux/termux-app/pull/5287>. The cause is described
    // there in exactly these terms: because the editor has no conventional
    // editable buffer, Honeyboard treats the cursor as being at the boundary
    // and suppresses KEYCODE_DPAD_RIGHT before the app ever receives it.
    //
    // The fix is to expose a VIRTUAL CONTEXT: sentinels flanking the cursor
    // purely so that neither side is ever empty. They exist only as IME
    // metadata — they are **never** written into the grid or the PTY, and not
    // one byte of them reaches the terminal.
    //
    // The sentinels come from the Unicode Private Use Area on purpose: they are
    // not letters, so autocorrect's word segmentation does not swallow them
    // along with the word. "\uE000ola" is still the word "ola" to anything
    // looking for a word — and still "I am not at a boundary" to anything
    // looking for a boundary.
    //
    // Why in TERMINAL mode too: that is precisely where the arrow keys are
    // indispensable, and precisely where the defect shows up — Samsung
    // keyboards ignore `TYPE_NULL` (Termux records this as well), so TERMINAL
    // mode is not protected by the inputType declaration.

    override fun getTextBeforeCursor(n: Int, flags: Int): CharSequence {
        if (n <= 0) return ""
        val real = if (modo().componeTexto) composing.toString() else ""
        val virtual = SENTINELA_ESQUERDA + real
        return if (virtual.length <= n) virtual else virtual.substring(virtual.length - n)
    }

    /**
     * There is no real text after the cursor — the cursor is always at the end
     * of what was typed. It returns the sentinel regardless: an empty answer
     * here is what makes Honeyboard suppress the right arrow key.
     */
    override fun getTextAfterCursor(n: Int, flags: Int): CharSequence {
        if (n <= 0) return ""
        return if (SENTINELA_DIREITA.length <= n) SENTINELA_DIREITA else SENTINELA_DIREITA.substring(0, n)
    }

    /** Selection belongs to the GRID, never to the IME — see `GridSelection`. */
    override fun getSelectedText(flags: Int): CharSequence? = null

    /**
     * The same virtual context, now whole and with the selection declared IN
     * THE MIDDLE. It has to tell the same story as [getTextBeforeCursor] and
     * [getTextAfterCursor]: an IME told the cursor is at the end by one and in
     * the middle by the other would go back to deciding it sits at a boundary.
     *
     * It returns context in BOTH modes — in TERMINAL mode just the sentinels,
     * with no text between them, which is the truth: there is no composition to
     * expose, but there is a cursor, and it is not at a boundary.
     */
    override fun getExtractedText(request: ExtractedTextRequest?, flags: Int): ExtractedText? {
        val real = if (modo().componeTexto) composing.toString() else ""
        val texto = SENTINELA_ESQUERDA + real + SENTINELA_DIREITA
        val cursor = SENTINELA_ESQUERDA.length + real.length
        return ExtractedText().apply {
            this.text = texto
            startOffset = 0
            partialStartOffset = -1
            partialEndOffset = -1
            selectionStart = cursor
            selectionEnd = cursor
        }
    }

    /**
     * Automatic capitalisation. `TerminalInputView` declares `CAP_SENTENCES` in
     * TEXTO mode; without implementing this, the promise was never kept — the
     * keyboard asked and [BaseInputConnection]'s default answered 0.
     *
     * It computes over the REAL text, never over the sentinels: they are not
     * punctuation and must not open a sentence.
     */
    override fun getCursorCapsMode(reqModes: Int): Int {
        if (!modo().componeTexto) return 0
        return TextUtils.getCapsMode(composing.toString(), composing.length, reqModes)
    }

    // --- batch editing -------------------------------------------------------
    //
    // The keyboard wraps an autocorrect word replacement in begin/endBatchEdit
    // so the editor applies everything at once. BaseInputConnection's default
    // answers `false`, which means "I cannot do batches" — and an IME that
    // hears that may give up on operations that depend on the batch, word
    // replacement among them.
    //
    // There is nothing to defer here (every method already acts immediately),
    // so the batch is merely COUNTED: the contract is accepted, nesting is
    // respected, and `endBatchEdit` reports whether any batch is still open —
    // which is literally what the documentation asks for.

    private var lotesAbertos = 0

    override fun beginBatchEdit(): Boolean {
        lotesAbertos++
        return true
    }

    override fun endBatchEdit(): Boolean {
        if (lotesAbertos > 0) lotesAbertos--
        return lotesAbertos > 0
    }

    /** Test only — never called by production code. */
    internal fun composingTextForTest(): String = composing.toString()

    // --- interno -------------------------------------------------------------

    private fun trocarComposicao(novo: String) {
        if (composing.toString() == novo) return
        composing.setLength(0)
        composing.append(novo)
        aoMudarComposicao(novo)
    }

    private fun limparComposicao() {
        jaEnviado = 0
        trocarComposicao("")
    }

    /** Sends the terminal whatever is left of the composition and has not gone out. */
    private fun despejarPendente() {
        if (composing.length > jaEnviado) {
            enviarTexto(composing.substring(jaEnviado))
            jaEnviado = composing.length
        }
    }

    private fun enviarTexto(texto: String) {
        if (texto.isEmpty()) return
        enviar(texto.toByteArray(Charsets.UTF_8))
        armDedupGuard(texto)
    }

    private fun enviar(bytes: ByteArray) {
        if (bytes.isEmpty()) return
        sink.send(bytes)
    }

    private fun armDedupGuard(enviado: String) {
        dedupQueue.clear()
        enviado.forEach(dedupQueue::addLast)
        dedupDeadlineNanos = nowNanos() + DEDUP_WINDOW_NANOS
    }

    private fun consumeIfDuplicate(char: Char): Boolean {
        if (dedupQueue.isEmpty() || nowNanos() > dedupDeadlineNanos) return false
        if (dedupQueue.first() != char) return false
        dedupQueue.removeFirst()
        return true
    }

    private companion object {
        /**
         * Sentinels for the virtual context. Unicode Private Use Area: they
         * never appear in real text, and they are not letters — so autocorrect
         * does not attach them to the word when segmenting. They are NEVER sent
         * to the terminal.
         */
        const val SENTINELA_ESQUERDA = "\uE000"
        const val SENTINELA_DIREITA = "\uE001"

        const val DEL_BYTE: Byte = 0x7f
        const val DEDUP_WINDOW_NANOS = 150_000_000L

        /** How many leading characters two strings have in common. */
        fun prefixoComum(a: CharSequence, b: CharSequence): Int {
            val limite = minOf(a.length, b.length)
            var i = 0
            while (i < limite && a[i] == b[i]) i++
            return i
        }
    }
}
