package dev.servercontrolpanel.feature.terminal.input

import android.content.Context
import android.util.AttributeSet
import android.view.View
import android.view.ViewTreeObserver
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import dev.servercontrolpanel.feature.terminal.prefs.TypingMode
import dev.servercontrolpanel.terminalengine.KeyByteEncoder

/**
 * The single Android input surface for the terminal keyboard. A plain [View],
 * not an `EditText` or Compose text field, which would create a second owner of
 * text state competing with the terminal grid. Its only text API surface is the
 * [TerminalInputConnection] it hands the IME.
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
     * How the keyboard behaves; see [TypingMode]. Changing it calls
     * [InputMethodManager.restartInput], since `EditorInfo` is only read when the
     * connection is created and the keyboard would otherwise stay in the old mode.
     */
    var typingMode: TypingMode = TypingMode.DEFAULT
        set(value) {
            if (field == value) return
            field = value
            onCompositionChange("")
            context.getSystemService(InputMethodManager::class.java)?.restartInput(this)
        }

    /**
     * Notifies the composition strip that the in-flight text changed, so
     * [TypingMode.TEXT] does not type blind while the keyboard holds the word.
     */
    var onCompositionChange: (String) -> Unit = {}

    /** Pending keyboard request, waiting for this view's window to regain focus. */
    private var awaitingFocus: ViewTreeObserver.OnWindowFocusChangeListener? = null

    init {
        isFocusable = true
        isFocusableInTouchMode = true
    }

    /**
     * Focuses this view and asks for the soft keyboard. The system only auto-shows
     * it once when the window gains focus, so after the user closes it, tapping the
     * grid must request it explicitly.
     *
     * Requested on this view rather than via Compose's `SoftwareKeyboardController`,
     * because the terminal `InputConnection` belongs to this view, which must be the
     * IME's served view.
     */
    fun showKeyboard() {
        requestFocus()
        if (hasWindowFocus()) {
            requestImeNow()
            return
        }
        // The window is not focused yet (e.g. "Show keyboard" in the options sheet,
        // a dialog window). `showSoftInput` on an unfocused window is silently
        // ignored, so wait for window focus, once.
        awaitingFocus?.let { viewTreeObserver.removeOnWindowFocusChangeListener(it) }
        val listener = ViewTreeObserver.OnWindowFocusChangeListener { hasFocus ->
            if (!hasFocus) return@OnWindowFocusChangeListener
            stopAwaitingFocus()
            requestFocus()
            requestImeNow()
        }
        awaitingFocus = listener
        viewTreeObserver.addOnWindowFocusChangeListener(listener)
    }

    private fun requestImeNow() {
        val imm = context.getSystemService(InputMethodManager::class.java) ?: return
        imm.showSoftInput(this, 0)
    }

    private fun stopAwaitingFocus() {
        val listener = awaitingFocus ?: return
        awaitingFocus = null
        if (viewTreeObserver.isAlive) viewTreeObserver.removeOnWindowFocusChangeListener(listener)
    }

    /**
     * Takes focus as soon as the window gains it, without needing a tap. The
     * screen's initial `requestFocus()` silently fails while the window is not yet
     * focused, and this view (covering the grid, `focusableInTouchMode`) would then
     * consume the first touch to get focus, so history scrolling would not work
     * until the user tapped. Only focus is requested here, not the keyboard.
     */
    override fun onAttachedToWindow() {
        super.onAttachedToWindow()
        if (requestFocus()) return
        awaitingFocus?.let { viewTreeObserver.removeOnWindowFocusChangeListener(it) }
        val listener = ViewTreeObserver.OnWindowFocusChangeListener { hasFocus ->
            if (!hasFocus) return@OnWindowFocusChangeListener
            stopAwaitingFocus()
            requestFocus()
        }
        awaitingFocus = listener
        viewTreeObserver.addOnWindowFocusChangeListener(listener)
    }

    override fun onDetachedFromWindow() {
        // A pending keyboard request must not outlive the view.
        stopAwaitingFocus()
        super.onDetachedFromWindow()
    }

    override fun onCheckIsTextEditor(): Boolean = true

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
        outAttrs.inputType = typingMode.inputType()
        outAttrs.imeOptions =
            EditorInfo.IME_FLAG_NO_EXTRACT_UI or EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_ACTION_NONE
        return TerminalInputConnection(
            view = this,
            sink = byteSink,
            cursorMode = cursorMode,
            // Read on every call: users switch modes with the keyboard open.
            mode = { typingMode },
            onCompositionChange = { text -> onCompositionChange(text) },
        )
    }
}
