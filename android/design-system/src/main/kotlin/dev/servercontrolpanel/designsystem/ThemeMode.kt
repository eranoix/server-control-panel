package dev.servercontrolpanel.designsystem

enum class ThemeMode(val id: String, val label: String) {

    LIGHT("light", "Light"),

    DARK("dark", "Dark"),

    SYSTEM("system", "System"),
    ;

    fun dark(systemDark: Boolean): Boolean = when (this) {
        LIGHT -> false
        DARK -> true
        SYSTEM -> systemDark
    }

    companion object {
        val DEFAULT: ThemeMode = SYSTEM

        fun byId(id: String?): ThemeMode = entries.firstOrNull { it.id == id } ?: DEFAULT
    }
}
