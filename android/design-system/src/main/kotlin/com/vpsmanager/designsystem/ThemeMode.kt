package com.vpsmanager.designsystem

/**
 * The three appearances the app offers.
 *
 * ## Why THREE and not a two-state switch
 * "Follow the system" is the behaviour the app ALWAYS had, and it is what most
 * people want: the device already switches by itself late in the day, and the
 * app follows. A light/dark switch would force precisely the people who like
 * the automatic behaviour to start deciding by hand — a regression dressed up
 * as a feature. So the manual choice COMES IN, and the automatic one STAYS
 * the default ([DEFAULT]).
 *
 * [id] is what goes to disk. It is a stable string on purpose: writing
 * `ordinal` would make a future reordering of this enum silently
 * reinterpret an already-saved preference.
 *
 * THE ORDER OF THE ENTRIES IS THE ORDER OF THE SELECTOR
 * ([ThemeModeSelector]): the two manual choices first, the automatic one
 * last — that is where "you handle it" sits in any three-way selector, and
 * where the finger goes looking when it wants to UNDO a manual choice.
 */
enum class ThemeMode(val id: String, val label: String) {

    /** Always light, even with the system in dark mode. */
    LIGHT("claro", "Light"),

    /** Always dark, even with the system in light mode. */
    DARK("escuro", "Dark"),

    /** Whatever the device is using right now — the default. */
    SYSTEM("sistema", "System"),
    ;

    /**
     * Whether this mode paints dark, given what the SYSTEM is using now.
     *
     * [systemDark] is only consulted by [SYSTEM]; [LIGHT] and [DARK]
     * ignore the system by definition — that is exactly what a manual choice
     * means.
     */
    fun dark(systemDark: Boolean): Boolean = when (this) {
        LIGHT -> false
        DARK -> true
        SYSTEM -> systemDark
    }

    companion object {
        /** The app's historical behaviour, and the default for anyone who never chose. */
        val DEFAULT: ThemeMode = SYSTEM

        /**
         * The mode stored under [id], or [DEFAULT] when the value is null or
         * unknown — an app downgrade, which does not know a newer id, falls
         * back to automatic instead of blowing up.
         */
        fun byId(id: String?): ThemeMode = entries.firstOrNull { it.id == id } ?: DEFAULT
    }
}
