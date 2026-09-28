package dev.servercontrolpanel.feature.terminal.input

import java.io.ByteArrayOutputStream

class RecordingByteSink : ByteSink {
    private val buffer = ByteArrayOutputStream()

    override fun send(bytes: ByteArray) {
        buffer.write(bytes)
    }

    fun bytes(): ByteArray = buffer.toByteArray()

    fun hex(): String = bytes().joinToString(" ") { "%02x".format(it) }

    fun isEmpty(): Boolean = buffer.size() == 0

    fun clear() {
        buffer.reset()
    }
}
