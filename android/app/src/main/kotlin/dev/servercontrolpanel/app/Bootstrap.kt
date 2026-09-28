package dev.servercontrolpanel.app

import android.content.Context
import android.util.Log
import java.io.File
import java.io.PrintWriter
import java.io.StringWriter

object Bootstrap {

    private const val TAG = "PanelBootstrap"
    private const val CRASH_FILE = "last-crash.txt"

    val initFailures = mutableListOf<String>()

    fun step(name: String, block: () -> Unit) {
        try {
            block()
        } catch (t: Throwable) {
            Log.e(TAG, "startup step failed: $name", t)
            initFailures += "$name: ${t.javaClass.simpleName}: ${t.message}"
        }
    }

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
            }
            previous?.uncaughtException(thread, error)
        }
    }

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
