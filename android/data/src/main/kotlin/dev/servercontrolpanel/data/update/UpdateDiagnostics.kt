package dev.servercontrolpanel.data.update

import android.content.Context
import java.io.File
import java.io.IOException

object UpdateDiagnostics {

    private const val FILE_NAME = "update-diagnostics.txt"
    private const val MAX_ENTRIES = 10
    private const val SEPARATOR = "\n---\n"

    fun record(context: Context, entry: String) {
        val file = file(context)
        try {
            val previous = if (file.isFile) file.readText().split(SEPARATOR).filter { it.isNotBlank() } else emptyList()
            val all = (listOf(entry.trim()) + previous).take(MAX_ENTRIES)
            file.writeText(all.joinToString(SEPARATOR))
        } catch (e: IOException) {
        } catch (e: SecurityException) {
        }
    }

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
        }
    }

    private fun file(context: Context) = File(context.applicationContext.filesDir, FILE_NAME)
}
