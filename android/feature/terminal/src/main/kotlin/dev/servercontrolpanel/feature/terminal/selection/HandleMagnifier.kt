package dev.servercontrolpanel.feature.terminal.selection

import android.view.View
import android.widget.Magnifier

class HandleMagnifier(private val host: View) {

    private var magnifier: Magnifier? = null

    fun show(xInHost: Float, rowY: Float) {
        if (!host.isAttachedToWindow) return
        val current = magnifier ?: Magnifier(host).also { magnifier = it }
        current.show(xInHost, rowY)
    }

    fun hide() {
        magnifier?.dismiss()
    }

    fun discard() {
        magnifier?.dismiss()
        magnifier = null
    }
}
