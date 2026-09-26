package com.vpsmanager.feature.terminal.input

/**
 * The only way keystroke bytes leave the input layer. This phase never
 * implements it against a socket: a later phase's production sink writes a
 * binary WebSocket frame to `/ws/shell`. Here it exists so the input layer's
 * byte contract can be proven in isolation, against a recording test double.
 */
fun interface ByteSink {
    fun send(bytes: ByteArray)
}
