package dev.servercontrolpanel.feature.terminal.transport

/**
 * Attach diagnostics in `logcat` under a single tag, so the real order of events
 * (engine size, resize, replay bytes) can be recovered on a device without `adb`.
 * `runCatching` because `android.util.Log` throws in JVM tests, and diagnostics
 * must never bring the terminal down.
 */
object TerminalDiag {
    const val TAG: String = "PanelTermAttach"

    /** On by default: the volume is a few lines per attach, not per byte. */
    @Volatile
    var enabled: Boolean = true

    fun log(message: String) {
        if (!enabled) return
        runCatching { android.util.Log.i(TAG, message) }
    }
}
