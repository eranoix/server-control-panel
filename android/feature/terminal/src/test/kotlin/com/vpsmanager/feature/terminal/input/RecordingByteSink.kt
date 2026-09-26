package com.vpsmanager.feature.terminal.input

import java.io.ByteArrayOutputStream

/**
 * Test-only [ByteSink] that appends every send to a growable buffer and
 * exposes it as raw bytes or a hex dump, so tests can assert exactly what
 * reached the sink and in what order.
 */
class RecordingByteSink : ByteSink {
    private val buffer = ByteArrayOutputStream()

    override fun send(bytes: ByteArray) {
        buffer.write(bytes)
    }

    fun bytes(): ByteArray = buffer.toByteArray()

    fun hex(): String = bytes().joinToString(" ") { "%02x".format(it) }

    fun isEmpty(): Boolean = buffer.size() == 0

    /** Clears the record, for a test that walks several scenarios in sequence. */
    fun clear() {
        buffer.reset()
    }
}
