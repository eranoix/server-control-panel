package com.vpsmanager.feature.terminal.keys

import android.view.KeyEvent
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.vpsmanager.designsystem.VpsmIcons
import androidx.compose.ui.text.font.FontWeight
import com.vpsmanager.terminalengine.KeyByteEncoder
import kotlinx.coroutines.withTimeoutOrNull

/**
 * The three states of the extra-keys row. The row is the ONLY permanent chrome
 * left below the grid (everything else moved into `TerminalOptionsSheet`), so
 * every dp it takes is a dp the grid does not get — hence three states rather
 * than a binary "show/hide".
 *
 * [COLAPSADA] is not "hidden": hiding it outright would make Ctrl+C
 * unreachable, and Ctrl+C is the number one reason anyone opens a terminal on
 * a phone. It is 32 dp holding the minimum set that keeps the terminal
 * usable — see [collapsedKeys].
 */
enum class ExtraKeysBarState(val heightDp: Dp, val visualRowHeightDp: Dp) {
    COLAPSADA(heightDp = 32.dp, visualRowHeightDp = 32.dp),
    UMA_LINHA(heightDp = 40.dp, visualRowHeightDp = 40.dp),
    DUAS_LINHAS(heightDp = 80.dp, visualRowHeightDp = 40.dp),
    ;

    /** The handle's tap cycle: A -> B -> C -> A. */
    fun next(): ExtraKeysBarState = when (this) {
        COLAPSADA -> UMA_LINHA
        UMA_LINHA -> DUAS_LINHAS
        DUAS_LINHAS -> COLAPSADA
    }

    /** One step up (more keys) when the handle is dragged vertically. */
    fun expanded(): ExtraKeysBarState = when (this) {
        COLAPSADA -> UMA_LINHA
        UMA_LINHA -> DUAS_LINHAS
        DUAS_LINHAS -> DUAS_LINHAS
    }

    /** One step down (fewer keys) when the handle is dragged vertically. */
    fun collapsed(): ExtraKeysBarState = when (this) {
        COLAPSADA -> COLAPSADA
        UMA_LINHA -> COLAPSADA
        DUAS_LINHAS -> UMA_LINHA
    }

    companion object {
        /**
         * The state a connected PHYSICAL keyboard imposes. With a real
         * keyboard, Esc/Tab/Ctrl/arrows already exist in hardware and the row
         * becomes pure lost grid — Blink does this by design, it is a
         * years-old open complaint against Termux, and JuiceSSH's own FAQ
         * acknowledges it as a bug precisely because it does NOT. Automatic,
         * but reversible through the handle: vanishing entirely annoys people
         * who want the arrow keys even with a keyboard, so the target is
         * [COLAPSADA], never "no row at all".
         */
        fun forHardwareKeyboard(present: Boolean): ExtraKeysBarState =
            if (present) COLAPSADA else UMA_LINHA
    }
}

/**
 * What a key on the row sends to the terminal. Its own type, rather than a
 * lambda hanging off each key, so that the key map — including each key's
 * second function on swipe-up — is inspectable data, exercisable on the JVM
 * through [runKeyAction] without standing up any composition.
 */
internal sealed interface KeyAction {
    /** A key with an Android keycode: goes through [KeyByteEncoder] with the pending modifiers merged in. */
    data class Code(val keyCode: Int) : KeyAction

    /** An explicit Ctrl+letter chord (Ctrl+C, Ctrl+D, Ctrl+L) — it does not depend on the sticky chip being armed. */
    data class Ctrl(val letter: Char) : KeyAction

    /** Raw UTF-8 text, for the keys that are a character rather than a key (`|`). */
    data class Literal(val text: String) : KeyAction

    /** Toggles the sticky Ctrl chip (OFF -> ARMED -> LOCKED -> OFF). */
    data object AlternarCtrl : KeyAction

    /** Toggles the sticky Alt chip. */
    data object AlternarAlt : KeyAction
}

/**
 * Runs a [KeyAction]. The single point that translates "the operator pressed
 * this key" into bytes, so that tap and swipe-up share exactly the same
 * pending-modifier semantics.
 */
internal fun runKeyAction(
    action: KeyAction,
    pendingModifiers: PendingModifiers,
    cursorMode: KeyByteEncoder.CursorMode,
    onSendBytes: (ByteArray) -> Unit,
) {
    when (action) {
        is KeyAction.Code -> tapExtraKey(action.keyCode, pendingModifiers, cursorMode, onSendBytes)
        is KeyAction.Ctrl -> {
            val keyCode = KeyEvent.KEYCODE_A + (action.letter.lowercaseChar() - 'a')
            val event = KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, keyCode, 0, KeyEvent.META_CTRL_ON)
            KeyByteEncoder.encode(event, cursorMode)?.let(onSendBytes)
            pendingModifiers.consumeAfterKeystroke()
        }
        is KeyAction.Literal -> {
            // A pending Alt becomes an ESC prefix, matching what
            // KeyByteEncoder does on the keycode path — a literal character
            // must not carry different modifier semantics than a mapped key.
            val body = action.text.toByteArray(Charsets.UTF_8)
            val bytes = if (pendingModifiers.isAltPending()) byteArrayOf(0x1b, *body) else body
            onSendBytes(bytes)
            pendingModifiers.consumeAfterKeystroke()
        }
        KeyAction.AlternarCtrl -> pendingModifiers.tapCtrl()
        KeyAction.AlternarAlt -> pendingModifiers.tapAlt()
    }
}

/** One key on the row: its label, its tap action and (optionally) its second function on swipe-up. */
internal data class ExtraKey(
    val label: String,
    val action: KeyAction,
    val swipeUp: KeyAction? = null,
    /** Repeats while held (arrows): without it, going back 30 characters is 30 taps. */
    val repeats: Boolean = false,
    /** Which sticky chip this key stands for, if it is Ctrl or Alt (it changes colour with the state). */
    val sticky: StickyKind? = null,
)

internal enum class StickyKind { CTRL, ALT }

/**
 * STATE A — the minimum that keeps the terminal USABLE with no other keys at
 * all.
 *
 * It deliberately diverges from the researched design (`⌃⌥ ⌃C ⌃D ⇥`) on two
 * points: `Esc` and `↑` come in, `⌃D` goes out. The reason: `Esc` and `↑` have
 * NO equivalent at all on the software keyboard (Gboard has neither Esc nor
 * arrows) — without them, leaving vim and repeating the last command are
 * impossible while the row is collapsed. `⌃D` goes because it stays reachable
 * (armed Ctrl chip plus `d` on the software keyboard) and because it is the
 * key that ENDS the session — the one you least want a single tap away on a
 * 32 dp target.
 */
internal val collapsedKeys: List<ExtraKey> = listOf(
    ExtraKey("Esc", KeyAction.Code(KeyEvent.KEYCODE_ESCAPE), swipeUp = KeyAction.Ctrl('c')),
    ExtraKey("Ctrl", KeyAction.AlternarCtrl, sticky = StickyKind.CTRL),
    ExtraKey("^C", KeyAction.Ctrl('c')),
    ExtraKey("Tab", KeyAction.Code(KeyEvent.KEYCODE_TAB), swipeUp = KeyAction.Ctrl('d')),
    ExtraKey("↑", KeyAction.Code(KeyEvent.KEYCODE_DPAD_UP), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_PAGE_UP), repeats = true),
)

/**
 * STATE B — eight keys, each with a second function on swipe-up. The pairing
 * of arrows with Home/End/PgUp/PgDn is the one from Termux's own advanced
 * `ExtraKeysInfo.java` example; swipe-up is its mechanism (`popup`), and the
 * only one that doubles capacity without costing a pixel of height.
 */
internal val oneRowKeys: List<ExtraKey> = listOf(
    ExtraKey("Esc", KeyAction.Code(KeyEvent.KEYCODE_ESCAPE), swipeUp = KeyAction.Ctrl('c')),
    ExtraKey("Tab", KeyAction.Code(KeyEvent.KEYCODE_TAB), swipeUp = KeyAction.Ctrl('d')),
    ExtraKey("Ctrl", KeyAction.AlternarCtrl, swipeUp = KeyAction.Ctrl('l'), sticky = StickyKind.CTRL),
    ExtraKey("Alt", KeyAction.AlternarAlt, swipeUp = KeyAction.Literal("|"), sticky = StickyKind.ALT),
    ExtraKey("←", KeyAction.Code(KeyEvent.KEYCODE_DPAD_LEFT), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_MOVE_HOME), repeats = true),
    ExtraKey("↓", KeyAction.Code(KeyEvent.KEYCODE_DPAD_DOWN), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_PAGE_DOWN), repeats = true),
    ExtraKey("↑", KeyAction.Code(KeyEvent.KEYCODE_DPAD_UP), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_PAGE_UP), repeats = true),
    ExtraKey("→", KeyAction.Code(KeyEvent.KEYCODE_DPAD_RIGHT), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_MOVE_END), repeats = true),
)

/** STATE C, top row: navigation and the shell punctuation the software keyboard buries behind pages. */
internal val twoRowsTopKeys: List<ExtraKey> = listOf(
    ExtraKey("Esc", KeyAction.Code(KeyEvent.KEYCODE_ESCAPE), swipeUp = KeyAction.Ctrl('c')),
    ExtraKey("/", KeyAction.Code(KeyEvent.KEYCODE_SLASH)),
    ExtraKey("-", KeyAction.Code(KeyEvent.KEYCODE_MINUS), swipeUp = KeyAction.Literal("|")),
    ExtraKey("Home", KeyAction.Code(KeyEvent.KEYCODE_MOVE_HOME)),
    ExtraKey("↑", KeyAction.Code(KeyEvent.KEYCODE_DPAD_UP), repeats = true),
    ExtraKey("End", KeyAction.Code(KeyEvent.KEYCODE_MOVE_END)),
    ExtraKey("PgUp", KeyAction.Code(KeyEvent.KEYCODE_PAGE_UP), repeats = true),
)

/** STATE C, bottom row. Seven keys per row: the number Termux converged on in both of its stock configurations. */
internal val twoRowsBottomKeys: List<ExtraKey> = listOf(
    ExtraKey("Tab", KeyAction.Code(KeyEvent.KEYCODE_TAB), swipeUp = KeyAction.Ctrl('d')),
    ExtraKey("Ctrl", KeyAction.AlternarCtrl, swipeUp = KeyAction.Ctrl('l'), sticky = StickyKind.CTRL),
    ExtraKey("Alt", KeyAction.AlternarAlt, sticky = StickyKind.ALT),
    ExtraKey("←", KeyAction.Code(KeyEvent.KEYCODE_DPAD_LEFT), repeats = true),
    ExtraKey("↓", KeyAction.Code(KeyEvent.KEYCODE_DPAD_DOWN), repeats = true),
    ExtraKey("→", KeyAction.Code(KeyEvent.KEYCODE_DPAD_RIGHT), repeats = true),
    ExtraKey("PgDn", KeyAction.Code(KeyEvent.KEYCODE_PAGE_DOWN), repeats = true),
)

/** Test tags — the row's height is a requirement, so it has to be measurable. */
const val EXTRA_KEYS_BAR_TAG = "barra-teclas-extras"

/** Test tag for the handle. */
const val EXTRA_KEYS_HANDLE_TAG = "alca-teclas-extras"

/** The handle's description for screen readers. */
const val HANDLE_DESCRIPTION = "Toggle key bar size"

/** The wait before a held key repeats for the first time. */
private const val INITIAL_REPEAT_DELAY_MILLIS = 400L

/** The interval between repeats — Termux's own `DEFAULT_LONG_PRESS_REPEAT_DELAY` value. */
private const val REPEAT_INTERVAL_MILLIS = 80L

/**
 * The extra keys bar, now with three states and a handle where the label used
 * to be.
 *
 * What it replaced: a scrollable `Row` of 9 buttons with an "Ocultar teclas ▾"
 * `TextButton` on a LINE OF ITS OWN above it — two rows of height to show one,
 * and the worst possible use of the screen's scarcest resource. The toggle is
 * now a 40 dp wide handle INSIDE the bar itself: a tap cycles A->B->C->A, a
 * vertical drag goes straight to the neighbouring state.
 *
 * Ctrl and Alt stay three-state sticky ([PendingModifiers]) — that was already
 * right and is more sophisticated than Termux; only where they live changes.
 *
 * A visual height of 40 dp with a 48 dp touch target: Compose expands a pointer
 * input node's hit-test area up to the system's minimum touchable size even
 * beyond the visual bounds, so the ink can be 40 dp without violating
 * Material's minimum target. Termux solves the same dilemma by violating the
 * target (a fixed 37.5 dp); here that is not necessary.
 */
@Composable
fun ExtraKeysBar(
    pendingModifiers: PendingModifiers,
    onSendBytes: (ByteArray) -> Unit,
    state: ExtraKeysBarState,
    onStateChange: (ExtraKeysBarState) -> Unit,
    modifier: Modifier = Modifier,
    cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    hasHardwareKeyboard: Boolean = false,
    /** Opens the attachment source sheet. See [BotaoDeAnexo]. */
    aoAnexar: () -> Unit = {},
) {
    // A physical keyboard being connected or disconnected repositions the row
    // on its own, once per transition — and only per transition, so that the
    // handle still has the last word afterwards.
    //
    // `jaAvaliou` is what stops the automation from becoming tyranny: on the
    // FIRST composition it only acts if a physical keyboard is present.
    // Without it, every entry into the screen (and every recomposition from
    // scratch) would reimpose UMA_LINHA over the state the operator had
    // chosen with the handle.
    var jaAvaliou by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(hasHardwareKeyboard) {
        if (hasHardwareKeyboard || jaAvaliou) {
            onStateChange(ExtraKeysBarState.forHardwareKeyboard(hasHardwareKeyboard))
        }
        jaAvaliou = true
    }

    val run: (KeyAction) -> Unit = { action ->
        runKeyAction(action, pendingModifiers, cursorMode, onSendBytes)
    }

    Surface(
        modifier = modifier.fillMaxWidth().height(state.heightDp).testTag(EXTRA_KEYS_BAR_TAG),
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
    ) {
        Row(modifier = Modifier.fillMaxSize()) {
            Column(modifier = Modifier.weight(1f).fillMaxHeight()) {
                when (state) {
                    ExtraKeysBarState.COLAPSADA -> KeyLine(collapsedKeys, state.visualRowHeightDp, pendingModifiers, run)
                    ExtraKeysBarState.UMA_LINHA -> KeyLine(oneRowKeys, state.visualRowHeightDp, pendingModifiers, run)
                    ExtraKeysBarState.DUAS_LINHAS -> {
                        KeyLine(twoRowsTopKeys, state.visualRowHeightDp, pendingModifiers, run)
                        KeyLine(twoRowsBottomKeys, state.visualRowHeightDp, pendingModifiers, run)
                    }
                }
            }
            BotaoDeAnexo(aoAnexar = aoAnexar)
            BarHandle(state = state, onStateChange = onStateChange)
        }
    }
}

/** One row of keys, split evenly — no key ends up off-screen, so there is no horizontal scrolling. */
@Composable
private fun KeyLine(
    keys: List<ExtraKey>,
    rowHeightDp: Dp,
    pendingModifiers: PendingModifiers,
    run: (KeyAction) -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth().height(rowHeightDp),
        horizontalArrangement = Arrangement.spacedBy(2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        keys.forEach { key ->
            KeyCap(
                key = key,
                pendingModifiers = pendingModifiers,
                run = run,
                modifier = Modifier.weight(1f).fillMaxHeight(),
            )
        }
    }
}

/**
 * One key. A single pointer loop covers all three gestures because they
 * compete for the SAME finger: a tap fires the primary action on `up`, a drag
 * upwards past the threshold fires the second function (and cancels the tap),
 * and holding repeats the primary every 80 ms (Termux's number) after an
 * initial 400 ms wait. Composing `detectTapGestures` with
 * `detectVerticalDragGestures` would not give you this: whichever recognises
 * first consumes the event, and the other never sees the whole gesture.
 */
@Composable
private fun KeyCap(
    key: ExtraKey,
    pendingModifiers: PendingModifiers,
    run: (KeyAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    val stickyState = when (key.sticky) {
        StickyKind.CTRL -> pendingModifiers.ctrl
        StickyKind.ALT -> pendingModifiers.alt
        null -> null
    }
    val container = when (stickyState) {
        ModifierArmState.ARMED -> MaterialTheme.colorScheme.tertiaryContainer
        ModifierArmState.LOCKED -> MaterialTheme.colorScheme.primary
        else -> Color.Transparent
    }
    val content = when (stickyState) {
        ModifierArmState.ARMED -> MaterialTheme.colorScheme.onTertiaryContainer
        ModifierArmState.LOCKED -> MaterialTheme.colorScheme.onPrimary
        else -> MaterialTheme.colorScheme.onSurface
    }
    // The extra dot distinguishes LOCKED from ARMED without relying on colour alone.
    val label = if (stickyState == ModifierArmState.LOCKED) "${key.label}•" else key.label

    Box(
        modifier = modifier
            .padding(horizontal = 1.dp, vertical = 2.dp)
            .background(container, RoundedCornerShape(6.dp))
            .semantics {
                if (key.swipeUp != null) {
                    contentDescription = "${key.label} — swipe up for the second function"
                }
            }
            .pointerInput(key) {
                val swipeThresholdPx = 20.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    down.consume()
                    var dy = 0f
                    var handled = false
                    var waitMillis = INITIAL_REPEAT_DELAY_MILLIS
                    while (true) {
                        val event = withTimeoutOrNull(if (key.repeats) waitMillis else Long.MAX_VALUE) {
                            awaitPointerEvent()
                        }
                        if (event == null) {
                            // Timed out with the finger still down: repeat.
                            run(key.action)
                            handled = true
                            waitMillis = REPEAT_INTERVAL_MILLIS
                            continue
                        }
                        val change = event.changes.firstOrNull { it.id == down.id } ?: break
                        dy += change.positionChange().y
                        if (!handled && key.swipeUp != null && dy <= -swipeThresholdPx) {
                            run(key.swipeUp)
                            handled = true
                        }
                        if (!change.pressed) {
                            if (!handled) run(key.action)
                            break
                        }
                        change.consume()
                    }
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            color = content,
            textAlign = TextAlign.Center,
            style = MaterialTheme.typography.labelLarge,
        )
    }
}

/**
 * The handle. It replaces the "Hide keys ▾" `TextButton` that took a whole
 * line just to announce that there was a line below it. A tap cycles the three
 * states; a vertical drag goes straight to the neighbour (upwards = more keys).
 */
@Composable
private fun BarHandle(state: ExtraKeysBarState, onStateChange: (ExtraKeysBarState) -> Unit) {
    val chevron = if (state == ExtraKeysBarState.DUAS_LINHAS) "⌄" else "⌃"
    Box(
        modifier = Modifier
            .width(40.dp)
            .fillMaxHeight()
            .testTag(EXTRA_KEYS_HANDLE_TAG)
            .semantics { contentDescription = HANDLE_DESCRIPTION }
            .pointerInput(state) {
                val dragThresholdPx = 16.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    down.consume()
                    var dy = 0f
                    var dragged = false
                    while (true) {
                        val event = awaitPointerEvent()
                        val change = event.changes.firstOrNull { it.id == down.id } ?: break
                        dy += change.positionChange().y
                        if (!dragged && dy <= -dragThresholdPx) {
                            onStateChange(state.expanded())
                            dragged = true
                        }
                        if (!dragged && dy >= dragThresholdPx) {
                            onStateChange(state.collapsed())
                            dragged = true
                        }
                        if (!change.pressed) {
                            if (!dragged) onStateChange(state.next())
                            break
                        }
                        change.consume()
                    }
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        Text(text = chevron, style = MaterialTheme.typography.titleMedium)
    }
}

/**
 * Encodes and sends one direct tap from the row (Esc/Tab/arrows/extra key),
 * merging [pendingModifiers]'s sticky Ctrl/Alt into the synthetic [KeyEvent]'s
 * metaState before [KeyByteEncoder.encode] runs — the exact same pre-encoding
 * merge [HardwareKeyHandler] applies to physical keystrokes, so both paths
 * share one encoder call site and one modifier semantics. `internal` (not
 * `private`) so `ExtraKeysBarActionTest` can drive it directly without needing a
 * Compose test rule.
 */
internal fun tapExtraKey(
    keyCode: Int,
    pendingModifiers: PendingModifiers,
    cursorMode: KeyByteEncoder.CursorMode,
    onSendBytes: (ByteArray) -> Unit,
) {
    var metaState = 0
    if (pendingModifiers.isCtrlPending()) metaState = metaState or KeyEvent.META_CTRL_ON
    if (pendingModifiers.isAltPending()) metaState = metaState or KeyEvent.META_ALT_ON
    val event = KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, keyCode, 0, metaState)
    val bytes = KeyByteEncoder.encode(event, cursorMode) ?: return
    onSendBytes(bytes)
    pendingModifiers.consumeAfterKeystroke()
}

/**
 * The attach button, at the end of the key row.
 *
 * ## What used to be here, and why it left
 *
 * This spot belonged to the input-mode switch ("abc"/"cmd"). It was promoted
 * here because it had been buried in the options sheet and the owner reported
 * the symptom of someone who does not know the feature exists. It went back
 * down at his request, now that the sheet **explains** what the mode does
 * ("Your keyboard's autocorrect and suggestions") instead of merely offering
 * two words — see `TerminalOptionsSheet`. Keeping both would be the same
 * control in two places, and this end of the row is expensive: it is the only
 * region that does not cost a real key.
 *
 * ## Why attaching earns the spot
 *
 * Getting an image or a file into the session had no path at all before — not
 * even a hidden one. And it is what everyday use asks for constantly: a
 * screenshot, a log, a file for Claude Code running in the session to read.
 *
 * ## Why it does NOT disable while offline
 *
 * The first version of this button locked while the session was down, on the
 * grounds that there would be nowhere to paste the path. The grounds were
 * false: `AnexoDoTerminal` already handles that case — the file uploads, it
 * sits in the [BarraDeAnexos], and it is the INSERTION of the path that waits
 * for the connection (see `AnexoInsercaoOfflineTest`, written after a path
 * really did evaporate on a reconnecting emulator).
 *
 * Locking here would remove exactly the case that code exists to serve:
 * picking the file while the network comes back. And it would leave two paths
 * to the same action under different rules, since the options sheet never
 * locked.
 */
@Composable
private fun BotaoDeAnexo(aoAnexar: () -> Unit) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        modifier = Modifier
            .fillMaxHeight()
            // 48dp is the minimum touch target. `Modifier.clickable` does NOT
            // enforce it — the Material components do, and this is a bare
            // Surface.
            .width(48.dp)
            .clickable(onClick = aoAnexar)
            .semantics { contentDescription = "Attach image or file to the session" },
    ) {
        Box(contentAlignment = Alignment.Center) {
            Icon(
                imageVector = VpsmIcons.Clipe,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
