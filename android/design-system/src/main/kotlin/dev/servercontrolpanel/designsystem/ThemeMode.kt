package dev.servercontrolpanel.designsystem

/**
 * The three appearances the app offers. Following the system stays the default
 * ([DEFAULT]); light and dark are manual overrides.
 *
 * [id] is what is persisted, a stable string so reordering the enum never
 * reinterprets a saved preference. Entry order is the order shown in
 * [ThemeModeSelector].
 */
enum class ThemeMode(val id: String, val label: String) {

    /** Always light, even with the system in dark mode. */
    LIGHT("light", "Light"),

    /** Always dark, even with the system in light mode. */
    DARK("dark", "Dark"),

    /** Whatever the device is using right now (the default). */
    SYSTEM("system", "System"),
    ;

    /** Whether this mode renders dark; [systemDark] only matters for [SYSTEM]. */
    fun dark(systemDark: Boolean): Boolean = when (this) {
        LIGHT -> false
        DARK -> true
        SYSTEM -> systemDark
    }

    companion object {
        /** Used when the owner never chose. */
        val DEFAULT: ThemeMode = SYSTEM

        /** The mode stored under [id], or [DEFAULT] for null or unknown ids (e.g. after a downgrade). */
        fun byId(id: String?): ThemeMode = entries.firstOrNull { it.id == id } ?: DEFAULT
    }
}
