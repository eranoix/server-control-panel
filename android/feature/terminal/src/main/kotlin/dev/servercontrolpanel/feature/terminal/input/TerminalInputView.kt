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

class TerminalInputView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
) : View(context, attrs) {

    var byteSink: ByteSink = ByteSink {  }

    var cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL

    var typingMode: TypingMode = TypingMode.DEFAULT
        set(value) {
            if (field == value) return
            field = value
            onCompositionChange("")
            context.getSystemService(InputMethodManager::class.java)?.restartInput(this)
        }

    var onCompositionChange: (String) -> Unit = {}

    private var awaitingFocus: ViewTreeObserver.OnWindowFocusChangeListener? = null

    init {
        isFocusable = true
        isFocusableInTouchMode = true
    }

    fun showKeyboard() {
        requestFocus()
        if (hasWindowFocus()) {
            requestImeNow()
            return
        }
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
            mode = { typingMode },
            onCompositionChange = { text -> onCompositionChange(text) },
        )
    }
}
