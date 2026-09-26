package com.vpsmanager.feature.terminal.selection

import android.view.ActionMode
import android.view.Menu
import android.view.MenuItem
import android.view.View
import androidx.compose.ui.geometry.Rect
import kotlin.math.roundToInt

/**
 * The SYSTEM's floating bar over the terminal — an `ActionMode` in
 * [ActionMode.TYPE_FLOATING] mode, the same object that shows up when you
 * select text in any Android field.
 *
 * **Why not the hand-drawn buttons that used to be here.** The app had a row
 * of its own with "Copy" and "Paste" as `TextButton`s, anchored to the foot of
 * the grid. It worked and it was wrong for three reasons: it sat far from the
 * selected text (the thumb travelled to the bottom of the screen to act on
 * something at the top), it did not speak the system's language (hard-coded
 * Portuguese labels, without the icons, the position and the animation the
 * user already knows) and it had no hierarchy at all — every button on the
 * same level, with no overflow menu.
 *
 * **What the system bar gives for free, and what it does not.** For free come
 * the position anchored to the selection, the animation, the overflow panel
 * and the already translated labels (`android.R.string.*`). What does NOT come
 * for free are other apps' actions ("Translate", "Search"): in AOSP it is the
 * `Editor` that gathers them, and an `ActionMode` requested by hand starts
 * with an EMPTY menu. Here they are assembled by [acoesDeOutrosApps],
 * replicating the `ProcessTextIntentActionsHandler`.
 *
 * The labels come from `android.R.string.*` on purpose: they are the SAME
 * texts, in the SAME language, that the device's own search field shows.
 *
 * **Which actions, and in which hierarchy**, is [itensDaBarra]'s business —
 * this file only hosts the action mode and dispatches the click. Read
 * [Destaque] before touching the set: what decides bar-versus-overflow is
 * `showAsAction`, and AOSP interprets it in a way that is not the ordinary
 * `Toolbar`'s.
 *
 * In Compose this would be the `LocalTextToolbar`, which only exists bound to
 * a text field — and this screen has none, by an architectural decision (see
 * the comment on `TerminalInputView`: an `Editable` would be a second owner of
 * the text state, which is the defect that stalled the previous attempt). So
 * the bar is requested by hand, from the `View` that is already in the tree.
 *
 * @param host the `View` that hosts the action mode. It is the [com.vpsmanager.feature.terminal.input.TerminalInputView],
 *   which occupies exactly the area of the grid — which is why the selection's
 *   pixel rectangle can go straight to `onGetContentRect`, with no space
 *   conversion.
 */
class TerminalActionMode(
    private val host: View,
    private val aoCopiar: () -> Unit,
    private val aoSelecionarTudo: () -> Unit,
    private val aoColar: () -> Unit,
    private val aoCompartilhar: () -> Unit,
    private val aoEnviarProTerminal: () -> Unit,
    private val aoFechar: () -> Unit,
    private val retanguloDaSelecao: () -> Rect,
    private val acoesDeOutrosApps: () -> List<AcaoDeOutroApp> = ::emptyList,
    private val aoUsarOutroApp: (AcaoDeOutroApp) -> Unit = {},
) {

    private var modo: ActionMode? = null

    // `finish()` fires `onDestroyActionMode`, which tells whoever closed it;
    // and whoever closed it usually clears the selection, which would call
    // `esconder()` again. Without this latch the recursion is infinite.
    private var fechandoPorDentro = false

    // The list of outside apps is frozen at the moment the menu is assembled,
    // and the click is resolved by INDEX into it. Re-querying the
    // `PackageManager` on the click could return a different order (an app
    // installed or removed in the meantime) and fire the wrong app.
    private var outrosAppsNoMenu: List<AcaoDeOutroApp> = emptyList()

    /** Shows the bar, or merely repositions the one already up. */
    fun mostrar() {
        val atual = modo
        if (atual != null) {
            atual.invalidateContentRect()
            return
        }
        modo = host.startActionMode(callback, ActionMode.TYPE_FLOATING)
    }

    /**
     * The selection has changed size or place: the bar needs to re-anchor.
     * `invalidateContentRect` is what makes the system recompute the position
     * and, during a handle drag, temporarily HIDE the bar — the same
     * behaviour as a text field, so the bar does not cover what is being
     * adjusted.
     */
    fun atualizar() {
        modo?.invalidateContentRect()
    }

    /** Is the bar up right now? Only the instrumented test asks — production reacts to events. */
    internal fun estaNoAr(): Boolean = modo != null

    /**
     * The `Menu` the SYSTEM assembled for this bar. Only the instrumented test
     * reads it — this is how it checks that the items reached the real menu
     * (and not just the [itensDaBarra] list) and fires the click down the real
     * path, `performIdentifierAction`.
     */
    internal fun menuNoAr(): Menu? = modo?.menu

    /** Takes the bar down without notifying back whoever asked for it. */
    fun esconder() {
        val atual = modo ?: return
        modo = null
        fechandoPorDentro = true
        atual.finish()
        fechandoPorDentro = false
    }

    private val callback = object : ActionMode.Callback2() {
        override fun onCreateActionMode(mode: ActionMode, menu: Menu): Boolean {
            outrosAppsNoMenu = acoesDeOutrosApps()
            montarMenu(menu, deOutrosApps = outrosAppsNoMenu)
            return true
        }

        /**
         * `false` = "I did not touch the menu", and it is the right answer
         * here because the set of items does NOT change from one showing to
         * the next (see [itensDaBarra]).
         *
         * This function used to read the clipboard on every preparation, so as
         * to disable "Paste" when it was empty. Two defects in that: the
         * disabled item did not go grey, it vanished
         * (`FloatingToolbar.getVisibleAndEnabledMenuItems` filters by
         * `isVisible() && isEnabled()`), and the read itself triggers, since
         * Android 12, the "<app> pasted from your clipboard" notice —
         * `getPrimaryClip()` is the call the system reports. In other words:
         * the app announced a paste that was not happening, every time the
         * user selected anything.
         */
        override fun onPrepareActionMode(mode: ActionMode, menu: Menu): Boolean = false

        override fun onActionItemClicked(mode: ActionMode, item: MenuItem): Boolean =
            when (acaoDoItem(item.itemId)) {
                AcaoDeSelecao.COPIAR -> {
                    aoCopiar()
                    // Copying ends the selection, as in a text field: the
                    // gesture is over, and leaving the highlight lit suggests
                    // there is still something to do with it.
                    mode.finish()
                    true
                }
                AcaoDeSelecao.SELECIONAR_TUDO -> {
                    // The only action that does NOT close the bar: it swaps
                    // the selection, and the next step (copy, share) acts on
                    // the new one — closing here would force selecting all
                    // over again.
                    aoSelecionarTudo()
                    true
                }
                AcaoDeSelecao.COLAR -> {
                    aoColar()
                    mode.finish()
                    true
                }
                AcaoDeSelecao.COMPARTILHAR -> {
                    aoCompartilhar()
                    mode.finish()
                    true
                }
                AcaoDeSelecao.ENVIAR_PRO_TERMINAL -> {
                    aoEnviarProTerminal()
                    mode.finish()
                    true
                }
                // Not one of ours: it may belong to an outside app
                // (`ACTION_PROCESS_TEXT`), or to nobody — and then `false`
                // lets the system handle it, rather than us firing the wrong
                // action.
                null -> {
                    val indice = indiceDeOutroApp(item.itemId, outrosAppsNoMenu.size)
                    if (indice == null) {
                        false
                    } else {
                        aoUsarOutroApp(outrosAppsNoMenu[indice])
                        mode.finish()
                        true
                    }
                }
            }

        override fun onDestroyActionMode(mode: ActionMode) {
            modo = null
            if (!fechandoPorDentro) aoFechar()
        }

        /**
         * Where the selected content is, so the system can land the bar ABOVE
         * it (or below, if it does not fit) instead of on top of it. Without
         * this override the system uses the bounds of the whole view — the
         * entire grid — and the bar appears at the top of the screen, far from
         * what was selected.
         */
        override fun onGetContentRect(mode: ActionMode, view: View, outRect: android.graphics.Rect) {
            val r = retanguloDaSelecao()
            outRect.set(
                r.left.roundToInt(),
                r.top.roundToInt(),
                r.right.roundToInt(),
                r.bottom.roundToInt(),
            )
        }
    }
}
