package dev.servercontrolpanel.data.update

import android.content.Context
import java.io.File
import java.io.IOException

/**
 * Update failures written to disk for the Diagnostics screen. Without `adb`,
 * `EXTRA_STATUS_MESSAGE` is the only explanation of an install failure, so it
 * must not live only in `Log.e`. A file rather than memory because the install
 * result receiver may run in a fresh process after the app was killed.
 */
object UpdateDiagnostics {

    private const val FILE_NAME = "atualizacao-diagnostico.txt"
    private const val MAX_ENTRIES = 10
    private const val SEPARATOR = "\n---\n"

    /** Adds [entry] at the TOP. The oldest ones fall off the end. */
    fun record(context: Context, entry: String) {
        val file = file(context)
        try {
            val previous = if (file.isFile) file.readText().split(SEPARATOR).filter { it.isNotBlank() } else emptyList()
            val all = (listOf(entry.trim()) + previous).take(MAX_ENTRIES)
            file.writeText(all.joinToString(SEPARATOR))
        } catch (e: IOException) {
            // Diagnostics that crash the app are worse than no diagnostics.
        } catch (e: SecurityException) {
        }
    }

    /** Everything stored, newest first, or null when there is nothing. */
    fun read(context: Context): String? = try {
        file(context).takeIf { it.isFile }?.readText()?.takeIf { it.isNotBlank() }
    } catch (e: IOException) {
        null
    } catch (e: SecurityException) {
        null
    }

    fun clear(context: Context) {
        try {
            file(context).delete()
        } catch (e: SecurityException) {
            // nothing to do
        }
    }

    private fun file(context: Context) = File(context.applicationContext.filesDir, FILE_NAME)
}
