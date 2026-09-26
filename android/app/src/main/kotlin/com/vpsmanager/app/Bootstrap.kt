package com.vpsmanager.app

import android.content.Context
import android.util.Log
import java.io.File
import java.io.PrintWriter
import java.io.StringWriter

/**
 * Startup diagnostics, so the app can report its own failure on a phone without adb or logcat.
 *
 * 1. [installCrashReporter] persists every uncaught exception to disk before the process dies;
 *    the launch screen shows it on the next boot.
 * 2. [step] lets each non-essential startup stage (phone account, WorkManager, notification
 *    channels, things a manufacturer may refuse) fail visibly without stopping the app.
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
            Log.e(TAG, "startup step failed: $name", t)
            initFailures += "$name: ${t.javaClass.simpleName}: ${t.message}"
        }
    }

    /**
     * Installs a handler that writes the stack trace to disk before the process
     * dies, chaining the previous handler so the crash behavior itself is unchanged.
     */
    fun installCrashReporter(context: Context) {
        val appContext = context.applicationContext
        val previous = Thread.getDefaultUncaughtExceptionHandler()
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
            previous?.uncaughtException(thread, error)
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
