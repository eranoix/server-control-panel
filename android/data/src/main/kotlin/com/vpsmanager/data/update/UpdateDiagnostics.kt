package com.vpsmanager.data.update

import android.content.Context
import java.io.File
import java.io.IOException

/**
 * What went wrong in the update, written to disk for the Diagnostics screen.
 *
 * ### Why this exists
 * The owner of this app has neither `adb` nor logcat: when the install fails,
 * the `PackageInstaller`'s `EXTRA_STATUS_MESSAGE` is the ONLY sentence that
 * says why ("signature does not match", "downgrade", "blocked by device
 * policy"). If it stays only in `Log.e`, it does not exist. Same stance as
 * [com.vpsmanager.data.update.UpdateStaging]: the diagnostics are built in,
 * not an extra.
 *
 * ### Why a file, and not memory
 * What writes here is usually the install result `BroadcastReceiver` — which
 * may run in a freshly created process, after the app has been killed. An
 * `object` holding a list in memory would lose exactly the most important
 * message.
 */
object UpdateDiagnostics {

    private const val FILE_NAME = "atualizacao-diagnostico.txt"
    private const val MAX_ENTRIES = 10
    private const val SEPARATOR = "\n---\n"

    /** Adds [entrada] at the TOP. The oldest ones fall off the end. */
    fun record(context: Context, entrada: String) {
        val file = file(context)
        try {
            val anteriores = if (file.isFile) file.readText().split(SEPARATOR).filter { it.isNotBlank() } else emptyList()
            val todas = (listOf(entrada.trim()) + anteriores).take(MAX_ENTRIES)
            file.writeText(todas.joinToString(SEPARATOR))
        } catch (e: IOException) {
            // Diagnostics that crash the app are worse than no diagnostics.
        } catch (e: SecurityException) {
            // idem
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
