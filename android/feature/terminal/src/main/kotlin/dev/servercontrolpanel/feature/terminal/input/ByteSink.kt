package dev.servercontrolpanel.feature.terminal.input

fun interface ByteSink {
    fun send(bytes: ByteArray)
}
