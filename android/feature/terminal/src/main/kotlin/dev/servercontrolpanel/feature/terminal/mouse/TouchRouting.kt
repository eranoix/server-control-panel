package dev.servercontrolpanel.feature.terminal.mouse

fun interface TouchRouting {

    fun programWantsMouse(): Boolean

    fun tapBelongsToApp(): Boolean = !programWantsMouse()
}
