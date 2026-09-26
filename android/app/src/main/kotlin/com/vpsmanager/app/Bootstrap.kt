package com.vpsmanager.app

import android.content.Context
import android.util.Log
import java.io.File
import java.io.PrintWriter
import java.io.StringWriter

/**
 * Startup diagnostics.
 *
 * Why this exists: this app was built end to end with no device
 * available — 412 unit tests green, zero real execution. The first boot
 * on a phone is, by construction, the first time any of these paths
 * actually runs. And the operator has only the phone: no `adb`, no
 * logcat, no way to see a stack trace.
 *
 * So the app has to be able to report its own failure. Two halves:
 *
 * 1. [installCrashReporter] persists every uncaught exception to disk
 *    BEFORE the process dies, and the launch screen shows the text on the
 *    next boot.
 * 2. [step] lets each startup stage fail without taking the app down —
 *    phone-account registration, WorkManager, notification channels and
 *    the like are things a manufacturer may refuse. None of them is
 *    essential for the first screen to open, so none has the right to
 *    stop the app from coming up. Failing visibly and carrying on beats
 *    dying silently in the user's face.
 */
object Bootstrap {

    private const val TAG = "VpsmBootstrap"
    private const val CRASH_FILE = "ultimo-crash.txt"

    /** Startup failures accumulated in this process, in the order they happened. */
    val initFailures = mutableListOf<String>()

    /**
     * Runs [block] isolating any failure. The app keeps coming up; the error
     * lands in [initFailures] and shows on the launch screen.
     */
    fun step(name: String, block: () -> Unit) {
        try {
            block()
        } catch (t: Throwable) {
            Log.e(TAG, "falha na etapa de inicializacao: $name", t)
            initFailures += "$name: ${t.javaClass.simpleName}: ${t.message}"
        }
    }

    /**
     * Installs a handler that writes the stack trace to disk before the process
     * dies, chaining the previous handler so the crash behaviour itself is
     * unchanged (the app still dies — it just stops dying mute).
     */
    fun installCrashReporter(context: Context) {
        val appContext = context.applicationContext
        val anterior = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, error ->
            try {
                val sw = StringWriter()
                error.printStackTrace(PrintWriter(sw))
                File(appContext.filesDir, CRASH_FILE).writeText(
                    buildString {
                        appendLine("thread: ${thread.name}")
                        appendLine("when: ${System.currentTimeMillis()}")
                        if (initFailures.isNotEmpty()) {
                            appendLine("init failures before the crash:")
                            initFailures.forEach { appendLine("  - $it") }
                        }
                        appendLine()
                        append(sw.toString())
                    },
                )
            } catch (_: Throwable) {
                // Writing the report must never make the original crash worse.
            }
            anterior?.uncaughtException(thread, error)
        }
    }

    /** Stack trace of the last crash, if any. */
    fun lastCrash(context: Context): String? =
        runCatching {
            File(context.applicationContext.filesDir, CRASH_FILE)
                .takeIf { it.exists() }
                ?.readText()
        }.getOrNull()

    fun clearLastCrash(context: Context) {
        runCatching { File(context.applicationContext.filesDir, CRASH_FILE).delete() }
    }
}
