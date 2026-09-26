package com.vpsmanager.feature.terminal.transport

/**
 * The attach logbook, in `logcat`, under a single tag.
 *
 * It exists because the owner of the device does NOT have `adb`: when he reports
 * "the history duplicated" or "the text came out squeezed", the only way to know
 * the real ORDER of events (the engine created at what size, when the resize
 * went out, when the replay arrived and with how many bytes) is to have it
 * recorded on the device itself. Measuring is what solved this defect; giving up
 * on measuring would be throwing the instrument away after using it once.
 *
 * `android.util.Log` does not exist in a pure JVM test (the Android Gradle
 * Plugin stub throws "not mocked"), and this path is called from inside code
 * covered by JVM tests — hence the `runCatching`. Diagnostics may never bring
 * the terminal down.
 */
object TerminalDiag {
    const val TAG: String = "VPSMTermAttach"

    /** On by default: the volume is a few lines per attach, not per byte. */
    @Volatile
    var enabled: Boolean = true

    fun log(mensagem: String) {
        if (!enabled) return
        runCatching { android.util.Log.i(TAG, mensagem) }
    }
}
