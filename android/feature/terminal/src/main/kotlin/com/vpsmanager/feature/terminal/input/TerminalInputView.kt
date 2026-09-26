package com.vpsmanager.feature.terminal.input

import android.content.Context
import android.util.AttributeSet
import android.view.View
import android.view.ViewTreeObserver
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import com.vpsmanager.feature.terminal.prefs.ModoDeDigitacao
import com.vpsmanager.terminalengine.KeyByteEncoder

/**
 * The single Android input surface for the terminal grid's keyboard.
 * Deliberately a plain [View] — NOT an `EditText`, NOT a Compose
 * `BasicTextField`/`TextField`. Those materialize an `Editable`/
 * `TextFieldValue` that becomes a second, competing owner of text state
 * alongside the terminal grid; that dual ownership is the defect that stalled
 * the previous mobile terminal attempt. This view owns no text at
 * all — its only Android-text-API surface is the [TerminalInputConnection]
 * it hands the IME.
 */
class TerminalInputView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
) : View(context, attrs) {

    /** Where composed/committed keystrokes go. Wired by the host screen before use. */
    var byteSink: ByteSink = ByteSink { /* no-op until the host screen wires a real sink */ }

    /** Which cursor-key escape family hardware/arrow keys encode to. */
    var cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL

    /**
     * How the keyboard should behave — see [ModoDeDigitacao].
     *
     * It replaces the old `inputTypeUnderTest`, which declared an ordinary text
     * field (`TYPE_CLASS_TEXT or TYPE_TEXT_FLAG_MULTI_LINE`) while the
     * `InputConnection` told the keyboard there was no text at all. The
     * contradiction between the two halves was the source of the wrong
     * corrections; see the header of [TerminalInputConnection].
     *
     * Changing the value **restarts input** ([InputMethodManager.restartInput]):
     * `EditorInfo` is only read when the connection is created, so without that
     * restart the keyboard would carry on in the previous mode until somebody
     * closed and reopened it — and the change would look as though it had not
     * worked.
     */
    var modoDeDigitacao: ModoDeDigitacao = ModoDeDigitacao.PADRAO
        set(valor) {
            if (field == valor) return
            field = valor
            aoMudarComposicao("")
            context.getSystemService(InputMethodManager::class.java)?.restartInput(this)
        }

    /**
     * Tells whoever draws the composition strip that the in-flight text changed.
     *
     * Without this, [ModoDeDigitacao.TEXTO] would be typing blind: the word is
     * held by the keyboard until it is confirmed, so the terminal screen shows
     * nothing while you type. It is the piece missing from Termux and the reason
     * a terminal usually just turns composition off altogether.
     */
    var aoMudarComposicao: (String) -> Unit = {}

    /** Pending keyboard request, waiting for this view's window to regain focus. */
    private var aguardandoFoco: ViewTreeObserver.OnWindowFocusChangeListener? = null

    init {
        isFocusable = true
        isFocusableInTouchMode = true
    }

    /**
     * Gives this view focus and ASKS the system for the soft keyboard.
     *
     * Without it the keyboard only ever appeared once, by accident: with
     * `windowSoftInputMode="adjustResize"` and `SOFT_INPUT_STATE_UNSPECIFIED`,
     * the system performs an auto-show when the window gains focus while a text
     * editor is focused — the [requestFocus] the screen calls when creating this
     * view. Once the keyboard was closed (back gesture), there was NO way back:
     * the auto-show does not repeat and nothing in the app called
     * `showSoftInput`. That was the reported defect — tapping the grid did not
     * bring the keyboard up.
     *
     * The request is made on the view itself, not through Compose's
     * `SoftwareKeyboardController`: the terminal's `InputConnection` belongs to
     * THIS view, so it is the one that has to be the IME's *served view*. Asking
     * through the Compose controller would raise the keyboard pointed at the
     * composition view, which has no editor at all.
     */
    fun showKeyboard() {
        requestFocus()
        if (hasWindowFocus()) {
            pedirImeAgora()
            return
        }
        // This view's window is NOT the focused one yet — the case of "Show
        // keyboard" in the options sheet, which is another window
        // (`ModalBottomSheet` is a dialog). `showSoftInput` on an unfocused
        // window is ignored SILENTLY: measured on the emulator, the button
        // closed the sheet and the keyboard did not come up, and not even
        // waiting a frame fixed it — window focus returns when it returns. So
        // the request waits for focus to arrive, exactly once.
        aguardandoFoco?.let { viewTreeObserver.removeOnWindowFocusChangeListener(it) }
        val listener = ViewTreeObserver.OnWindowFocusChangeListener { temFoco ->
            if (!temFoco) return@OnWindowFocusChangeListener
            pararDeAguardarFoco()
            requestFocus()
            pedirImeAgora()
        }
        aguardandoFoco = listener
        viewTreeObserver.addOnWindowFocusChangeListener(listener)
    }

    private fun pedirImeAgora() {
        val imm = context.getSystemService(InputMethodManager::class.java) ?: return
        imm.showSoftInput(this, 0)
    }

    private fun pararDeAguardarFoco() {
        val listener = aguardandoFoco ?: return
        aguardandoFoco = null
        if (viewTreeObserver.isAlive) viewTreeObserver.removeOnWindowFocusChangeListener(listener)
    }

    /**
     * Secures focus as soon as the WINDOW gains focus — without depending on a
     * tap.
     *
     * ## The defect this fixes
     *
     * The screen calls `requestFocus()` when creating this view. At that instant
     * the window is still **not the focused one** (navigation has only just
     * changed destination), and in touch mode `requestFocus()` on an unfocused
     * window fails — silently, returning a `false` nobody looked at.
     *
     * The damage is not the keyboard: it is the GESTURE. This view covers the
     * entire grid and is `focusableInTouchMode`, so while it has no focus the
     * first touch is consumed to acquire it and **never reaches** Compose's drag
     * recognisers, which live in the `Box` above it. The operator described
     * exactly this: *"I cannot scroll the history down when it starts; it only
     * unlocks when I tap the writing area"*. Tapping the writing area was what
     * granted the focus the initial `requestFocus()` had failed to get.
     *
     * The fix is to ask again once window focus arrives — the same mechanism
     * [showKeyboard] already used for the options-sheet button, now also on
     * entering the screen. Without raising the keyboard: here only focus
     * matters.
     */
    override fun onAttachedToWindow() {
        super.onAttachedToWindow()
        if (requestFocus()) return
        aguardandoFoco?.let { viewTreeObserver.removeOnWindowFocusChangeListener(it) }
        val listener = ViewTreeObserver.OnWindowFocusChangeListener { temFoco ->
            if (!temFoco) return@OnWindowFocusChangeListener
            pararDeAguardarFoco()
            requestFocus()
        }
        aguardandoFoco = listener
        viewTreeObserver.addOnWindowFocusChangeListener(listener)
    }

    override fun onDetachedFromWindow() {
        // A keyboard request that was never served cannot outlive the view
        // that made it.
        pararDeAguardarFoco()
        super.onDetachedFromWindow()
    }

    override fun onCheckIsTextEditor(): Boolean = true

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
        outAttrs.inputType = modoDeDigitacao.inputType()
        outAttrs.imeOptions =
            EditorInfo.IME_FLAG_NO_EXTRACT_UI or EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_ACTION_NONE
        return TerminalInputConnection(
            view = this,
            sink = byteSink,
            cursorMode = cursorMode,
            // Read on every call rather than captured: people switch modes
            // with the keyboard open, and a copy would go stale in silence.
            modo = { modoDeDigitacao },
            aoMudarComposicao = { texto -> aoMudarComposicao(texto) },
        )
    }
}
