package dev.servercontrolpanel.feature.terminal.keys

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
import dev.servercontrolpanel.designsystem.PanelIcons
import androidx.compose.ui.text.font.FontWeight
import dev.servercontrolpanel.terminalengine.KeyByteEncoder
import kotlinx.coroutines.withTimeoutOrNull

enum class ExtraKeysBarState(val heightDp: Dp, val visualRowHeightDp: Dp) {
    COLLAPSED(heightDp = 32.dp, visualRowHeightDp = 32.dp),
    ONE_ROW(heightDp = 40.dp, visualRowHeightDp = 40.dp),
    TWO_ROWS(heightDp = 80.dp, visualRowHeightDp = 40.dp),
    ;

    fun next(): ExtraKeysBarState = when (this) {
        COLLAPSED -> ONE_ROW
        ONE_ROW -> TWO_ROWS
        TWO_ROWS -> COLLAPSED
    }

    fun expanded(): ExtraKeysBarState = when (this) {
        COLLAPSED -> ONE_ROW
        ONE_ROW -> TWO_ROWS
        TWO_ROWS -> TWO_ROWS
    }

    fun collapsed(): ExtraKeysBarState = when (this) {
        COLLAPSED -> COLLAPSED
        ONE_ROW -> COLLAPSED
        TWO_ROWS -> ONE_ROW
    }

    companion object {
        fun forHardwareKeyboard(present: Boolean): ExtraKeysBarState =
            if (present) COLLAPSED else ONE_ROW
    }
}

internal sealed interface KeyAction {
    data class Code(val keyCode: Int) : KeyAction

    data class Ctrl(val letter: Char) : KeyAction

    data class Literal(val text: String) : KeyAction

    data object ToggleCtrl : KeyAction

    data object ToggleAlt : KeyAction
}

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
            val body = action.text.toByteArray(Charsets.UTF_8)
            val bytes = if (pendingModifiers.isAltPending()) byteArrayOf(0x1b, *body) else body
            onSendBytes(bytes)
            pendingModifiers.consumeAfterKeystroke()
        }
        KeyAction.ToggleCtrl -> pendingModifiers.tapCtrl()
        KeyAction.ToggleAlt -> pendingModifiers.tapAlt()
    }
}

internal data class ExtraKey(
    val label: String,
    val action: KeyAction,
    val swipeUp: KeyAction? = null,
    val repeats: Boolean = false,
    val sticky: StickyKind? = null,
)

internal enum class StickyKind { CTRL, ALT }

internal val collapsedKeys: List<ExtraKey> = listOf(
    ExtraKey("Esc", KeyAction.Code(KeyEvent.KEYCODE_ESCAPE), swipeUp = KeyAction.Ctrl('c')),
    ExtraKey("Ctrl", KeyAction.ToggleCtrl, sticky = StickyKind.CTRL),
    ExtraKey("^C", KeyAction.Ctrl('c')),
    ExtraKey("Tab", KeyAction.Code(KeyEvent.KEYCODE_TAB), swipeUp = KeyAction.Ctrl('d')),
    ExtraKey("↑", KeyAction.Code(KeyEvent.KEYCODE_DPAD_UP), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_PAGE_UP), repeats = true),
)

internal val oneRowKeys: List<ExtraKey> = listOf(
    ExtraKey("Esc", KeyAction.Code(KeyEvent.KEYCODE_ESCAPE), swipeUp = KeyAction.Ctrl('c')),
    ExtraKey("Tab", KeyAction.Code(KeyEvent.KEYCODE_TAB), swipeUp = KeyAction.Ctrl('d')),
    ExtraKey("Ctrl", KeyAction.ToggleCtrl, swipeUp = KeyAction.Ctrl('l'), sticky = StickyKind.CTRL),
    ExtraKey("Alt", KeyAction.ToggleAlt, swipeUp = KeyAction.Literal("|"), sticky = StickyKind.ALT),
    ExtraKey("←", KeyAction.Code(KeyEvent.KEYCODE_DPAD_LEFT), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_MOVE_HOME), repeats = true),
    ExtraKey("↓", KeyAction.Code(KeyEvent.KEYCODE_DPAD_DOWN), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_PAGE_DOWN), repeats = true),
    ExtraKey("↑", KeyAction.Code(KeyEvent.KEYCODE_DPAD_UP), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_PAGE_UP), repeats = true),
    ExtraKey("→", KeyAction.Code(KeyEvent.KEYCODE_DPAD_RIGHT), swipeUp = KeyAction.Code(KeyEvent.KEYCODE_MOVE_END), repeats = true),
)

internal val twoRowsTopKeys: List<ExtraKey> = listOf(
    ExtraKey("Esc", KeyAction.Code(KeyEvent.KEYCODE_ESCAPE), swipeUp = KeyAction.Ctrl('c')),
    ExtraKey("/", KeyAction.Code(KeyEvent.KEYCODE_SLASH)),
    ExtraKey("-", KeyAction.Code(KeyEvent.KEYCODE_MINUS), swipeUp = KeyAction.Literal("|")),
    ExtraKey("Home", KeyAction.Code(KeyEvent.KEYCODE_MOVE_HOME)),
    ExtraKey("↑", KeyAction.Code(KeyEvent.KEYCODE_DPAD_UP), repeats = true),
    ExtraKey("End", KeyAction.Code(KeyEvent.KEYCODE_MOVE_END)),
    ExtraKey("PgUp", KeyAction.Code(KeyEvent.KEYCODE_PAGE_UP), repeats = true),
)

internal val twoRowsBottomKeys: List<ExtraKey> = listOf(
    ExtraKey("Tab", KeyAction.Code(KeyEvent.KEYCODE_TAB), swipeUp = KeyAction.Ctrl('d')),
    ExtraKey("Ctrl", KeyAction.ToggleCtrl, swipeUp = KeyAction.Ctrl('l'), sticky = StickyKind.CTRL),
    ExtraKey("Alt", KeyAction.ToggleAlt, sticky = StickyKind.ALT),
    ExtraKey("←", KeyAction.Code(KeyEvent.KEYCODE_DPAD_LEFT), repeats = true),
    ExtraKey("↓", KeyAction.Code(KeyEvent.KEYCODE_DPAD_DOWN), repeats = true),
    ExtraKey("→", KeyAction.Code(KeyEvent.KEYCODE_DPAD_RIGHT), repeats = true),
    ExtraKey("PgDn", KeyAction.Code(KeyEvent.KEYCODE_PAGE_DOWN), repeats = true),
)

const val EXTRA_KEYS_BAR_TAG = "extra-keys-bar"

const val EXTRA_KEYS_HANDLE_TAG = "extra-keys-handle"

const val HANDLE_DESCRIPTION = "Toggle key bar size"

private const val INITIAL_REPEAT_DELAY_MILLIS = 400L

private const val REPEAT_INTERVAL_MILLIS = 80L

@Composable
fun ExtraKeysBar(
    pendingModifiers: PendingModifiers,
    onSendBytes: (ByteArray) -> Unit,
    state: ExtraKeysBarState,
    onStateChange: (ExtraKeysBarState) -> Unit,
    modifier: Modifier = Modifier,
    cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    hasHardwareKeyboard: Boolean = false,
    onAttach: () -> Unit = {},
) {
    var alreadyEvaluated by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(hasHardwareKeyboard) {
        if (hasHardwareKeyboard || alreadyEvaluated) {
            onStateChange(ExtraKeysBarState.forHardwareKeyboard(hasHardwareKeyboard))
        }
        alreadyEvaluated = true
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
                    ExtraKeysBarState.COLLAPSED -> KeyLine(collapsedKeys, state.visualRowHeightDp, pendingModifiers, run)
                    ExtraKeysBarState.ONE_ROW -> KeyLine(oneRowKeys, state.visualRowHeightDp, pendingModifiers, run)
                    ExtraKeysBarState.TWO_ROWS -> {
                        KeyLine(twoRowsTopKeys, state.visualRowHeightDp, pendingModifiers, run)
                        KeyLine(twoRowsBottomKeys, state.visualRowHeightDp, pendingModifiers, run)
                    }
                }
            }
            AttachButton(onAttach = onAttach)
            BarHandle(state = state, onStateChange = onStateChange)
        }
    }
}

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

@Composable
private fun BarHandle(state: ExtraKeysBarState, onStateChange: (ExtraKeysBarState) -> Unit) {
    val chevron = if (state == ExtraKeysBarState.TWO_ROWS) "⌄" else "⌃"
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

@Composable
private fun AttachButton(onAttach: () -> Unit) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        modifier = Modifier
            .fillMaxHeight()
            .width(48.dp)
            .clickable(onClick = onAttach)
            .semantics { contentDescription = "Attach image or file to the session" },
    ) {
        Box(contentAlignment = Alignment.Center) {
            Icon(
                imageVector = PanelIcons.Clip,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
