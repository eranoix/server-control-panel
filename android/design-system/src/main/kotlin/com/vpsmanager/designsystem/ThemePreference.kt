package com.vpsmanager.designsystem

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The appearance the device's owner chose, stored on the device itself.
 *
 * ## Why SharedPreferences and not DataStore
 * The rest of this app's preferences (terminal font size, for instance) live
 * in DataStore, and rightly so: they are read AFTER the screen already
 * exists, so an asynchronous read costs nothing. This is the only one that
 * has to be answered BEFORE the first frame — the theme is decided in the
 * root composition, and an asynchronous read means composing with the
 * default, receiving the value an instant later and recomposing: the app
 * opens light and turns dark in the owner's face. That "flash" is exactly the
 * defect this file exists in order not to have.
 *
 * `SharedPreferences` is the sanctioned API for reading a preference
 * SYNCHRONOUSLY (the file is loaded whole on the first query and stays in
 * memory), and it is the same choice `AppCompatDelegate` itself makes to
 * persist night mode. A `runBlocking { dataStore.data.first() }` would give
 * the same result by blocking the main thread on an API that documents not
 * doing that — swapping the right tool for a workaround.
 *
 * The value lives in a [StateFlow] whose INITIAL value is already the one on
 * disk: anyone collecting with `collectAsStateWithLifecycle()` gets the right
 * mode on the first read, with no intermediate frame.
 *
 * [prefs] is an injectable seam for tests — the [get] singleton is cached per
 * process and would leak values between `@Test`s.
 */
class ThemePreference(
    private val prefs: SharedPreferences,
) {

    constructor(context: Context) : this(
        context.applicationContext.getSharedPreferences(NOME_ARQUIVO, Context.MODE_PRIVATE),
    )

    private val _mode = MutableStateFlow(ThemeMode.porId(prefs.getString(CHAVE_MODO, null)))

    /** The current appearance. The value is already right before the first composition. */
    val mode: StateFlow<ThemeMode> = _mode.asStateFlow()

    /** Synchronous read, for whoever needs the value outside a composition. */
    fun current(): ThemeMode = _mode.value

    /**
     * Writes the choice down and publishes it.
     *
     * `apply()` (asynchronous) and not `commit()`: the in-memory source of
     * truth is the [StateFlow], which has already been updated — waiting for
     * the disk would only stall the touch. The write itself is ordered and
     * survives the end of the process.
     */
    fun set(mode: ThemeMode) {
        prefs.edit().putString(CHAVE_MODO, mode.id).apply()
        _mode.value = mode
    }

    companion object {
        private const val NOME_ARQUIVO = "vpsm_aparencia"
        private const val CHAVE_MODO = "modo_tema"

        @Volatile
        private var instancia: ThemePreference? = null

        /**
         * The process-wide instance. It is a singleton because the preference
         * is one for the whole app: the `MainActivity`, the share Activity and
         * the diagnostics screen ALL need to read the same value and see the
         * same change — two instances would give two truths and one of them
         * would stay stale until the next boot.
         */
        fun get(context: Context): ThemePreference =
            instancia ?: synchronized(this) {
                instancia ?: ThemePreference(context).also { instancia = it }
            }
    }
}
