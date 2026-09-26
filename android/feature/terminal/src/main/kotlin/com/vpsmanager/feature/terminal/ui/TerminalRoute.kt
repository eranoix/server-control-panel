package com.vpsmanager.feature.terminal.ui

import android.content.res.Configuration
import android.widget.Toast
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.foundation.background
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.layout.layout
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.Density
import com.vpsmanager.terminalengine.TerminalScrollState
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.createSavedStateHandle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import com.vpsmanager.feature.terminal.attach.TerminalAttachment
import com.vpsmanager.feature.terminal.geometry.ScreenAnchor
import com.vpsmanager.feature.terminal.geometry.GridGeometry
import com.vpsmanager.feature.terminal.input.TerminalInputView
import com.vpsmanager.feature.terminal.transport.ConnectionState
import com.vpsmanager.feature.terminal.keys.ExtraKeysBar
import com.vpsmanager.feature.terminal.keys.ExtraKeysBarState
import com.vpsmanager.feature.terminal.keys.HardwareKeyHandler
import com.vpsmanager.feature.terminal.keys.PendingModifiers
import com.vpsmanager.feature.terminal.mouse.MouseEventEncoder
import com.vpsmanager.feature.terminal.mouse.MouseReportGestureController
import com.vpsmanager.feature.terminal.mouse.TouchRouting
import com.vpsmanager.feature.terminal.power.BATTERY_EXEMPTION_DIALOG_TAG
import com.vpsmanager.feature.terminal.power.BATTERY_EXEMPTION_EXPLANATION
import com.vpsmanager.feature.terminal.power.BATTERY_EXEMPTION_LABEL
import com.vpsmanager.feature.terminal.power.openBatteryExemptionRequest
import com.vpsmanager.feature.terminal.power.isBatteryOptimizationExempt
import com.vpsmanager.feature.terminal.prefs.TerminalFontSizePreference
import com.vpsmanager.feature.terminal.prefs.VisibleRows
import com.vpsmanager.feature.terminal.prefs.VisibleRowsPreference
import com.vpsmanager.feature.terminal.prefs.TypingMode
import com.vpsmanager.feature.terminal.prefs.TypingModePreference
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacing
import com.vpsmanager.feature.terminal.prefs.TerminalScrollback
import com.vpsmanager.feature.terminal.prefs.TerminalScrollbackPreference
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacingPreference
import com.vpsmanager.feature.terminal.render.GlyphAtlas
import com.vpsmanager.feature.terminal.render.TerminalCanvas
import com.vpsmanager.feature.terminal.render.TerminalCellMetrics
import com.vpsmanager.feature.terminal.render.computeTerminalCellMetrics
import com.vpsmanager.feature.terminal.render.currentTerminalPalette
import com.vpsmanager.feature.terminal.scroll.ScrollPositionOverlay
import com.vpsmanager.feature.terminal.scroll.ScrollbackGestureController
import com.vpsmanager.feature.terminal.scroll.canvasScrollGesture
import com.vpsmanager.feature.terminal.selection.OtherAppAction
import com.vpsmanager.feature.terminal.transport.TerminalDiag
import com.vpsmanager.feature.terminal.selection.CanvasTapTarget
import com.vpsmanager.feature.terminal.selection.CellHitTester
import com.vpsmanager.feature.terminal.selection.CopyAction
import com.vpsmanager.feature.terminal.selection.GridSelection
import com.vpsmanager.feature.terminal.selection.GridSelectionHolder
import com.vpsmanager.feature.terminal.selection.PasteAction
import com.vpsmanager.feature.terminal.selection.SelectionGestureController
import com.vpsmanager.feature.terminal.selection.SelectionOverlay
import com.vpsmanager.feature.terminal.selection.DOUBLE_TAP
import com.vpsmanager.feature.terminal.selection.TRIPLE_TAP
import com.vpsmanager.feature.terminal.selection.TerminalActionMode
import com.vpsmanager.feature.terminal.selection.SelectedText
import com.vpsmanager.feature.terminal.selection.otherAppActions
import com.vpsmanager.feature.terminal.selection.canvasDragGestures
import com.vpsmanager.feature.terminal.selection.canvasTapGesture
import com.vpsmanager.feature.terminal.selection.shareText
import com.vpsmanager.feature.terminal.selection.processTextIntent
import com.vpsmanager.feature.terminal.selection.routeCanvasDrag
import com.vpsmanager.feature.terminal.selection.routeCanvasTap
import com.vpsmanager.feature.terminal.selection.selectionBounds
import com.vpsmanager.feature.terminal.selection.selectLine
import com.vpsmanager.feature.terminal.selection.selectWord
import com.vpsmanager.feature.terminal.selection.selectAll
import com.vpsmanager.terminalengine.CellSnapshot
import com.vpsmanager.terminalengine.MouseGeometry
import com.vpsmanager.terminalengine.TerminalModes
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Screen-reader description for the detail bar's back arrow. It is the only
 * bar in the app that has one — a top-level destination carries the shell's
 * hamburger there instead — so the shell test reads it from here to tell the
 * two headers apart without depending on a loose literal.
 */
const val BACK_DESCRIPTION = "Back"

/** Test tag for the session button in the top bar. */
const val SESSIONS_TAG = "sessoes-terminal"

/** What the session button says in the top bar. */
const val LABEL_SESSIONS = "Session"

/**
 * What shows up when "Paste" is tapped with nothing copied. The item is
 * pinned to the bar (the app owner asked for that), so it has to say why
 * nothing happened rather than simply doing nothing — see `PasteAction`.
 */
const val NOTICE_NOTHING_TO_PASTE = "There is nothing copied to paste"


/**
 * The live terminal screen. [TerminalViewModel] is constructed from
 * `createSavedStateHandle()` so a process-death relaunch reconstructs the
 * same session name from the restored back-stack entry — see the ViewModel's
 * own doc comment for why that, not a plain constructor argument, is what
 * survives the app being killed.
 *
 * **This screen's column is deliberately very short: top bar, grid, key bar.
 * Nothing else.** It used to be
 * `TopAppBar + ConnectionBanner + MouseReportingToggleRow + FontSizeControlRow
 * + ScrollbackPanel + grid + "Hide keys" + keys` — 224.8 dp of chrome above
 * the grid on a 914 dp device (measured on the emulator at 420 dpi), 160.8 dp
 * of which were three EPISODIC controls given PERMANENT height. All three
 * moved to [TerminalOptionsSheet], which costs 0 dp while closed; the
 * connection banner already appeared only when it had something to say and
 * still does; and the "Hide keys" label on a row of its own became the handle
 * inside [ExtraKeysBar] itself. **The "Copy/Paste" row the app drew is gone
 * too** — the ones offering those actions now are the SYSTEM's floating bar
 * ([TerminalActionMode]), which appears glued to the selection and dismisses
 * itself.
 *
 * The grid uses `weight(1f)`, not `fillMaxSize()`. That is not a styling
 * detail: with `fillMaxSize()` inside a `Column`, the grid consumed ALL the
 * remaining space and the key bar was measured with 0 dp of height left — it
 * existed in the composition and showed up in not a single pixel. That was
 * the reason the extra keys were unreachable on the device.
 *
 * IME/edge-to-edge insets: this composable applies NEITHER `imePadding()` nor
 * a second `consumeWindowInsets` call. `AppNavHost`'s `NavHost` modifier
 * already applies both exactly once, wrapping every destination including
 * this one — adding a second consumption point here would double-apply the
 * inset (the same reasoning [TerminalCanvas]'s own doc comment gives for
 * never touching insets itself).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TerminalRoute(
    onBack: () -> Unit,
    onSwitchSession: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val viewModel: TerminalViewModel = viewModel(
        factory = viewModelFactory {
            initializer { TerminalViewModel(createSavedStateHandle()) }
        },
    )
    val connectionState by viewModel.connectionState.collectAsStateWithLifecycle()
    // The BANNER reads the delayed state (a 1.5 s grace period), the LOGIC
    // reads the real state just above. A reconnection that settles in ~1 s —
    // the normal case of coming back to the app — never lights a banner at all.
    val bannerState by viewModel.bannerState.collectAsStateWithLifecycle()
    val bridgeOrigin by viewModel.bridgeCommandOrigin.collectAsStateWithLifecycle()
    val pendingTyping by viewModel.pendingTyping.collectAsStateWithLifecycle()
    val typingDiscarded by viewModel.typingDiscarded.collectAsStateWithLifecycle()
    val isStalled by viewModel.isStalled.collectAsStateWithLifecycle()

    val density = LocalDensity.current
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()

    var sessionsOpen by remember { mutableStateOf(false) }
    // The session list comes from the SAME place the list screen uses
    // ([SessionListViewModel] over `TerminalSessionsSource`). There is no
    // second source of truth about sessions — what there is is a second
    // surface showing the first.
    val sessionsViewModel: SessionListViewModel = viewModel()
    val sessionsState by sessionsViewModel.uiState.collectAsStateWithLifecycle()

    var batteryExempt by remember { mutableStateOf(isBatteryOptimizationExempt(context)) }
    var exemptionDialogOpen by remember { mutableStateOf(false) }

    // One observer, two DIFFERENT events — and the difference matters:
    //
    // ON_START → RECONNECT. It was measured that Android 15+ blocks the app's
    //   network ~5.7 s after it leaves the foreground and tears its sockets
    //   down; once blocked, the reconnection loop does not even get to emit a
    //   SYN. On the way back, then, what exists is always a dead connection
    //   and a loop sleeping off its backoff — waiting for a heartbeat to fail
    //   would only postpone the inevitable. ON_START is the right event
    //   because it only fires when the screen really was invisible, not on
    //   every dialog that opens over it.
    //
    // ON_RESUME → RE-READ the battery exemption. It has to be here, and this
    //   was found out by testing on the emulator: the SYSTEM dialog that
    //   grants the exemption never STOPS this screen (it is a translucent
    //   activity on top), so ON_START does not fire on the way back from it.
    //   With the refresh only on ON_START, the sheet went on offering an
    //   exemption the person had just granted.
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> viewModel.onReturnedToForeground()
                Lifecycle.Event.ON_RESUME -> batteryExempt = isBatteryOptimizationExempt(context)
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }
    // Font size persisted purely on-device (see
    // TerminalFontSizePreference's own doc comment on why this never touches
    // the server); `remember(fontSizeSp)` below is what turns a change here
    // into a rebuilt GlyphAtlas and recomputed cell metrics.
    val fontSizePreference = remember { TerminalFontSizePreference(context) }
    val fontSizeSp by fontSizePreference.fontSizeSp.collectAsStateWithLifecycle(
        initialValue = TerminalFontSizePreference.DEFAULT_FONT_SIZE_SP,
    )
    // Line spacing: same discipline as the font — a presentation preference,
    // device-only. It goes into the metrics `remember` alongside the font
    // size because it changes the cell height, hence the GRID (number of
    // rows), hence the resize that goes to the server.
    val lineSpacingPreference = remember { TerminalLineSpacingPreference(context) }
    val lineSpacing by lineSpacingPreference.lineSpacing.collectAsStateWithLifecycle(
        initialValue = TerminalLineSpacing.DEFAULT,
    )
    // Typing mode: defines the contract the view declares to the device
    // keyboard. It does not enter the metrics — it touches no cell — but it
    // changes the `inputType`, so swapping the value restarts IME input
    // (see TerminalInputView.typingMode).
    // Conversation history: read here and handed to the ViewModel, which uses
    // it at the moment the engine is CREATED (scrollback size is fixed at
    // creation) and to decide how much log to fetch from the server.
    //
    // The STEP, and not the stored integer, is what travels down from here: a
    // value from an older ladder (200 thousand) would reserve a capacity the
    // fetch would never fill, and would leave the options sheet with no step
    // lit. `byRows` snaps it; the rest of the flow only ever sees steps.
    val scrollbackPreference = remember { TerminalScrollbackPreference(context) }
    val savedScrollback by scrollbackPreference.lines.collectAsStateWithLifecycle(
        initialValue = TerminalScrollback.DEFAULT.lines,
    )
    val scrollbackLines = TerminalScrollback.byRows(savedScrollback).lines
    // It arrives from the DataStore after the ViewModel is constructed; the
    // engine is only born once the grid has been measured, so handing the
    // value over here is in time.
    viewModel.scrollbackLines = scrollbackLines
    // How many rows fit on screen. Unlike the font size, this is the number
    // the person actually has in mind ("I want to see the whole `docker ps`");
    // the type size becomes a consequence. See [VisibleRows].
    val visibleRowsPreference = remember { VisibleRowsPreference(context) }
    val visibleRowsValue by visibleRowsPreference.lines.collectAsStateWithLifecycle(
        initialValue = VisibleRows.DEFAULT.lines,
    )
    val visibleRows = VisibleRows.byRows(visibleRowsValue)

    val typingModePreference = remember { TypingModePreference(context) }
    val currentTypingMode by typingModePreference.mode.collectAsStateWithLifecycle(
        initialValue = TypingMode.DEFAULT,
    )
    // The word the keyboard is composing and has not yet handed to the
    // terminal. It only exists in TEXT mode; empty, the strip emits no node.
    var pendingComposition by remember { mutableStateOf("") }
    // The current theme's grid colours (background, default text, cursor and
    // the light theme's legibility guard). Read up here because a change of
    // theme has to repaint the grid in the same recomposition that repaints
    // the rest.
    val terminalPalette = currentTerminalPalette
    // The AVAILABLE HEIGHT, used to derive the type size when the number of
    // rows is fixed. It starts at zero and arrives with the first
    // measurement; until then the chosen size stands, and the first
    // composition already draws something legible instead of waiting on a
    // measure.
    var heightForRowsPx by remember { mutableStateOf(0) }

    val metrics = remember(density, fontSizeSp, lineSpacing, visibleRows, heightForRowsPx) {
        if (visibleRows == VisibleRows.AUTOMATIC || heightForRowsPx <= 0) {
            computeCellMetrics(density, fontSizeSp, lineSpacing)
        } else {
            bodyThatFits(
                lines = visibleRows.lines,
                heightPx = heightForRowsPx,
                density = density,
                lineSpacing = lineSpacing,
            )
        }
    }
    val glyphAtlas = remember(metrics) {
        GlyphAtlas(
            cellWidthPx = metrics.cellWidthPx,
            cellHeightPx = metrics.cellHeightPx,
            textSizePx = metrics.textSizePx,
        )
    }
    // GlyphAtlas is a fixed-size Bitmap pair rebuilt (not resized) on every
    // font-size change -- without this, the OLD atlas's two Bitmaps would
    // never be reclaimed and total atlas memory would grow unbounded across
    // repeated font-size changes instead of staying at "exactly one atlas's
    // worth" at a time.
    DisposableEffect(glyphAtlas) {
        onDispose {
            glyphAtlas.narrowBitmap.recycle()
            glyphAtlas.wideBitmap.recycle()
        }
    }
    val snapshotState = remember { mutableStateOf<CellSnapshot?>(null) }
    // Sticky Ctrl/Alt state shared between ExtraKeysBar's own chip taps and
    // HardwareKeyHandler, so a modifier armed from the on-screen row applies
    // equally to the row's other keys and to a physical keystroke.
    val pendingModifiers = remember { PendingModifiers() }
    val hardwareKeyHandler = remember(viewModel) {
        HardwareKeyHandler(viewModel.byteSink, pendingModifiers = pendingModifiers)
    }

    // Grid selection is deliberately its own holder + polled state, not a
    // StateFlow the ViewModel owns -- selection is pure canvas gesture UI
    // state, never written from anywhere IME/composition-related (see
    // GridSelection's own doc comment on the independence invariant
    // SelectionComposeIndependenceTest proves).
    val selectionHolder = remember { GridSelectionHolder() }
    val selectionState = remember { mutableStateOf<GridSelection?>(null) }
    var gridCols by remember { mutableStateOf(1) }
    var gridRows by remember { mutableStateOf(1) }

    // The two insets the grid's arithmetic needs. Read here as OBJECTS and
    // queried down below inside the `Modifier.layout`: reading `getBottom`
    // during measurement is a LAYOUT-PHASE state read, so the grid remeasures
    // itself while the keyboard slides, without recomposing this whole
    // function on every frame of the animation. It is the same mechanism
    // `imePadding()` uses internally.
    val keyboardInsets = WindowInsets.ime
    val navBarInsets = WindowInsets.navigationBars

    /** The height the grid has with the keyboard closed. For the options sheet only. */
    var heightWithoutKeyboardPx by remember { mutableStateOf(0) }

    val hitTesterProvider: () -> CellHitTester = remember(metrics) {
        {
            CellHitTester(
                cellWidthPx = metrics.cellWidthPx.toFloat(),
                cellHeightPx = metrics.cellHeightPx.toFloat(),
                cols = gridCols,
                rows = gridRows,
                // No `originY`: the grid is OFFSET during placement, and
                // the pointer coordinates that arrive here already come in
                // the offset space. Correcting again would count the offset
                // twice. See the grid box's `Modifier.layout`.
            )
        }
    }

    // The modes the REMOTE PROGRAM turned on. Re-read every frame, together
    // with the snapshot: an `htop` that opens turns mouse reporting on and one
    // that closes turns it off, without telling anyone — the app has to ask,
    // not remember.
    var terminalModes by remember { mutableStateOf(TerminalModes.NONE) }

    // The system's floating bar. It is born together with the
    // `TerminalInputView` (that view is what hosts the `ActionMode`), down
    // below in the `AndroidView`.
    val selectionBar = remember { mutableStateOf<TerminalActionMode?>(null) }

    val selectionController = remember(hitTesterProvider, selectionHolder) {
        SelectionGestureController(hitTesterProvider, selectionHolder) { selection ->
            // Android is what draws the bar, and it has to be CALLED —
            // nothing observes this holder on its own.
            val bar = selectionBar.value
            if (selection == null) bar?.hide() else bar?.show()
        }
    }

    // No state and no preference: who owns the gesture is decided, gesture
    // by gesture, by the mode the REMOTE PROGRAM turned on. See
    // [TouchRouting].
    val touchRouting = remember(viewModel) {
        TouchRouting { viewModel.currentModes().mouseTracking }
    }

    // The encoding itself belongs to the VT emulator — it knows the mode and
    // the format the program asked for. This lambda only hands over the grid's
    // geometry.
    val mouseEncoder = remember(viewModel, metrics) {
        MouseEventEncoder { action, position, button, anyButtonPressed ->
            viewModel.encodeMouse(
                action = action,
                button = button,
                positionXPx = position.x,
                positionYPx = position.y,
                geometry = MouseGeometry(
                    cellWidthPx = metrics.cellWidthPx,
                    cellHeightPx = metrics.cellHeightPx,
                    screenWidthPx = gridCols * metrics.cellWidthPx,
                    screenHeightPx = gridRows * metrics.cellHeightPx,
                ),
                anyButtonPressed = anyButtonPressed,
            )
        }
    }
    val mouseReportController = remember(mouseEncoder, viewModel) {
        MouseReportGestureController(mouseEncoder, viewModel.byteSink)
    }
    val canvasDragTarget = remember(touchRouting, selectionController, mouseReportController) {
        routeCanvasDrag(touchRouting, selectionController, mouseReportController)
    }

    // The fourth gesture: a vertical drag scrolls the history. Where it
    // scrolls to is NOT the app's choice — it comes from the emulator's real
    // state, the same discipline as the mouse and the paste. See
    // [decideScroll].
    val scrollTarget = remember(viewModel, metrics) {
        ScrollbackGestureController(
            modes = { viewModel.currentModes() },
            geometry = {
                MouseGeometry(
                    cellWidthPx = metrics.cellWidthPx,
                    cellHeightPx = metrics.cellHeightPx,
                    screenWidthPx = gridCols * metrics.cellWidthPx,
                    screenHeightPx = gridRows * metrics.cellHeightPx,
                )
            },
            scrollViewport = viewModel::scrollViewport,
            // After scrolling, is there anywhere left to go? This is what
            // makes the fling stop at the top of the history instead of
            // grinding against it.
            canScrollViewport = { lines ->
                val state = viewModel.currentScrollState()
                if (lines < 0) state.offset > 0 else !state.atEnd
            },
            sendBytes = viewModel.byteSink::send,
            encodeMouse = { action, button, xPx, yPx, geometry ->
                viewModel.encodeMouse(
                    action = action,
                    button = button,
                    positionXPx = xPx,
                    positionYPx = yPx,
                    geometry = geometry,
                    anyButtonPressed = false,
                )
            },
        )
    }
    // The input view is created by the `AndroidView` down below; holding on
    // to it here is what allows the keyboard to be ASKED FOR from outside it
    // (a tap on the grid, a button on the options sheet) — the request has to
    // leave from the view that owns the terminal's InputConnection, see
    // TerminalInputView.showKeyboard().
    val inputView = remember { mutableStateOf<TerminalInputView?>(null) }
    val requestKeyboard: () -> Unit = { inputView.value?.showKeyboard() }

    /**
     * The tap on the grid when the gesture belongs to the app. One tap raises
     * the keyboard, two select the word, three select the logical line — the
     * language every Android text field already speaks, and that was missing
     * here.
     */
    val tapKeyboardTarget = remember(selectionController, viewModel, hitTesterProvider) {
        CanvasTapTarget { position, taps ->
            val snapshot = viewModel.currentSnapshot()
            if (snapshot == null || taps < DOUBLE_TAP) {
                selectionController.clearSelection()
                inputView.value?.showKeyboard()
                return@CanvasTapTarget
            }
            // The measured grid and the snapshot can disagree for a frame
            // during a resize; the cell is clamped to the snapshot's bounds,
            // since the snapshot is the source of the text about to be
            // selected.
            val cell = hitTesterProvider().hitTest(position)
            val line = cell.row.coerceIn(0, snapshot.rows - 1)
            val column = cell.col.coerceIn(0, snapshot.cols - 1)
            selectionController.setSelection(
                if (taps >= TRIPLE_TAP) {
                    selectLine(snapshot, line)
                } else {
                    selectWord(snapshot, line, column)
                },
            )
        }
    }
    val canvasTapTarget = remember(touchRouting, tapKeyboardTarget, mouseReportController) {
        routeCanvasTap(touchRouting, tapKeyboardTarget, mouseReportController)
    }
    val clipboardManager = LocalClipboardManager.current
    val copyAction = remember(selectionHolder, viewModel) {
        CopyAction(
            snapshotProvider = viewModel::currentSnapshot,
            selectionProvider = { selectionHolder.selection },
            clipboardWrite = { text -> clipboardManager.setText(AnnotatedString(text)) },
        )
    }
    val pasteAction = remember(viewModel, context) {
        PasteAction(
            clipboardRead = { clipboardManager.getText()?.text },
            sendPaste = viewModel::sendPaste,
            // "Paste" is pinned to the bar, so it is the only item that
            // may have nothing to do. A Toast, and not a Snackbar: the
            // floating bar lives in a window of its own, above this one — a
            // Snackbar anchored to the Scaffold would appear BEHIND it.
            onContentMissing = {
                Toast.makeText(context, NOTICE_NOTHING_TO_PASTE, Toast.LENGTH_SHORT).show()
            },
        )
    }
    // The text under the selection, read at the instant of the click — the
    // source for the two overflow-menu actions that consume the selection
    // without going through the clipboard.
    val selectedText = remember(selectionHolder, viewModel) {
        SelectedText(
            snapshotProvider = viewModel::currentSnapshot,
            selectionProvider = { selectionHolder.selection },
        )
    }
    val share: () -> Unit = remember(selectedText, context) {
        { selectedText.use { text -> shareText(context, text) } }
    }
    // Sending the selection back to the remote program goes through the SAME
    // `sendPaste` as a paste — it is the VT emulator that decides whether to
    // wrap it in `ESC[200~`/`ESC[201~`, according to the mode the program
    // asked for (see `PasteAction`). Pushing the bytes out around this path
    // would reintroduce the two defects that path exists to avoid.
    val sendToTerminal: () -> Unit = remember(selectedText, viewModel) {
        { selectedText.use(viewModel::sendPaste) }
    }
    // "Translate", "Search", "Read aloud" — what the device's apps offer over
    // plain text. The one enumerating them is the bar, as it opens, and not
    // this composition: that way installing or removing an app is reflected
    // without recreating the screen.
    val openInOtherApp = remember(selectedText, context) {
        { action: OtherAppAction ->
            selectedText.use { text ->
                context.startActivity(processTextIntent(action, text))
            }
        }
    }

    // A PHYSICAL keyboard is attached: `hardKeyboardHidden ==
    // HARDKEYBOARDHIDDEN_NO` is the reading the framework exposes, and it
    // reacts to a Bluetooth one being connected/disconnected in real time
    // (the Configuration changes, the composition re-reads).
    val configuration = LocalConfiguration.current
    val hasHardwareKeyboard = configuration.hardKeyboardHidden == Configuration.HARDKEYBOARDHIDDEN_NO
    var keysBarState by remember { mutableStateOf(ExtraKeysBarState.ONE_ROW) }
    var optionsOpen by remember { mutableStateOf(false) }
    // Session attachment (file/image/photo): only the sheet's switch lives
    // here; the rest (upload, progress, inserting the path) sits entirely in
    // TerminalAttachment, so this function does not swell again.
    var attachmentOpen by remember { mutableStateOf(false) }

    // Renderer polling loop, deliberately decoupled from onBytes cadence (a
    // throughput finding): this reads a plain poll method, not a per-write
    // StateFlow, so redraw rate tracks device frame cadence rather than
    // server write volume. Selection is polled the same way, from the same
    // holder [SelectionGestureController] writes to on every drag event.
    // Scroll position, re-read in the same frame as the snapshot:
    // libghostty-vt warns, explicitly, that there is NO notification of a
    // scroll change — whoever draws the position asks for it.
    var scrollState by remember { mutableStateOf(TerminalScrollState.AT_END) }
    // How much history there was at the instant the owner left the bottom.
    // If the total grows after that, new output arrived while they were
    // reading — and the screen did NOT jump to show it, so it has to say that
    // it exists.
    var totalWhenLeftEnd by remember { mutableStateOf(0L) }
    var hasNewOutput by remember { mutableStateOf(false) }

    LaunchedEffect(viewModel) {
        while (isActive) {
            withFrameNanos {
                snapshotState.value = viewModel.currentSnapshot()
                selectionState.value = selectionHolder.selection
                terminalModes = viewModel.currentModes()

                val now = viewModel.currentScrollState()
                if (now.atEnd) {
                    // Back at the bottom: there is nothing "new" out of sight.
                    totalWhenLeftEnd = now.total
                    hasNewOutput = false
                } else {
                    if (scrollState.atEnd) totalWhenLeftEnd = now.total
                    hasNewOutput = now.total > totalWhenLeftEnd
                }
                scrollState = now
            }
        }
    }

    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                // HALF the default height (64dp), at the owner's request.
                // On this screen the bar is a frame: what matters is the
                // terminal grid, and every dp the header gives back is one
                // more line of output visible on a phone.
                //
                // THE FONT DOES NOT CHANGE — that was asked for explicitly.
                // Only the space around it shrinks.
                expandedHeight = 32.dp,
                title = { Text(text = viewModel.sessionName) },
                // A DETAIL screen: a real back arrow — it leaves the
                // session and hands you back to the list. A top-level
                // destination has no such arrow (there the spot belongs to
                // the shell's hamburger), and it is the arrow that tells the
                // two headers apart at a glance.
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = BACK_DESCRIPTION,
                        )
                    }
                },
                actions = {
                    // The drag-mode echo ("Selection") used to live here,
                    // and lost its reason to exist along with the mouse
                    // preference. The space is worth more as a SESSION
                    // SWITCH: there are 20+ sessions, and until now switching
                    // meant going back to the list, which throws the grid away
                    // and redoes the attach.
                    TextButton(
                        onClick = { sessionsOpen = true },
                        modifier = Modifier.testTag(SESSIONS_TAG),
                    ) {
                        Text(text = LABEL_SESSIONS)
                    }
                    IconButton(onClick = { optionsOpen = true }) {
                        Icon(
                            imageVector = Icons.Filled.MoreVert,
                            contentDescription = OPTIONS_DESCRIPTION,
                        )
                    }
                },
            )
        },
    ) { innerPadding ->
        Column(modifier = Modifier.fillMaxSize().padding(innerPadding)) {
            ConnectionBanner(
                state = bannerState,
                isStalled = isStalled,
                typingDiscarded = typingDiscarded,
            )
            BridgeBanner(origin = bridgeOrigin)
            PendingTypingBanner(text = pendingTyping)
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    // THE BACKGROUND BELONGS TO THE TERMINAL, and it
                    // paints the WHOLE area.
                    //
                    // The grid can be placed lower than the top (see
                    // ScreenAnchor): whatever is left above it is drawn by
                    // nobody, and showed up in the application's surface
                    // colour — a grey band in a black terminal, which reads as
                    // a defect and not as a blank line. Painting here costs
                    // one rectangle per frame and makes the whole area,
                    // visually, terminal.
                    .background(Color(0xFF000000 or terminalPalette.defaultBg.toLong()))
                    // CLIP BEFORE the `layout`: the node reports the
                    // visible height, so the clip confines the part that
                    // spills upwards instead of letting it paint over the
                    // title bar.
                    .clipToBounds()
                    // ── THE GRID DOES NOT SHRINK WITH THE KEYBOARD ──────
                    //
                    // It is MEASURED at the height it would have with the
                    // keyboard closed and PLACED shifted upwards, showing the
                    // last lines. To the parent, the node still has the
                    // visible height — nothing else on screen moves.
                    //
                    // That way the number of rows does not change when the
                    // keyboard comes up, and it is the change in the number of
                    // rows that used to duplicate the history (see
                    // GridGeometry). There is no heuristic and no memory:
                    // what `imePadding()` took away is added back by the exact
                    // value, frame by frame.
                    //
                    // Shifting during PLACEMENT and not while drawing is what
                    // makes touch, selection and the `TerminalInputView` come
                    // along with no correction of their own: pointer
                    // coordinates follow placement.
                    .layout { measurable, constraints ->
                        val covered = GridGeometry.coveredByKeyboard(
                            imePx = keyboardInsets.getBottom(this),
                            navBarPx = navBarInsets.getBottom(this),
                        )
                        val fullHeight = constraints.maxHeight + covered
                        val placed = measurable.measure(
                            constraints.copy(minHeight = fullHeight, maxHeight = fullHeight),
                        )
                        layout(placed.width, constraints.maxHeight) {
                            // ── WHAT HAS TO STAY VISIBLE IS THE CURSOR ───
                            //
                            // The temptation is to anchor to the bottom —
                            // always show the last lines that fit. It is
                            // wrong, and the mistake is easy to miss: in a
                            // freshly opened session the content lives at the
                            // TOP of an almost empty grid, and anchoring to
                            // the bottom shows the last 27 lines, which are 27
                            // blank lines. The whole screen goes away with the
                            // history intact, and that was exactly the report:
                            // "nothing shows up for me any more".
                            //
                            // What the keyboard must not cover is the line
                            // being typed on. So the offset is the MINIMUM
                            // that brings the cursor inside the visible area,
                            // and zero when it is already there. A terminal
                            // behaves like a text field: it scrolls to the
                            // insertion point, not to the end of the document.
                            //
                            // The snapshot is read HERE, during placement, and
                            // not during measurement: moving the cursor
                            // re-PLACES the grid, without re-measuring
                            // anything. Measuring on every key would drag the
                            // `TerminalInputView` and the overlays along, on
                            // every key.
                            // ── THE MARGIN BELOW THE CURSOR ──────────────────
                            //
                            // Aligning the BOTTOM of the cursor's line with
                            // the lower edge looks right and cuts the screen
                            // off: in a TUI there is almost always content
                            // AFTER the cursor. In Claude Code, the input box
                            // is drawn over three lines — top border, text,
                            // bottom border — and the cursor sits on the
                            // middle one. Butting the cursor against the edge
                            // ate the box's bottom line, which was the report:
                            // "the keyboard is cutting off the writing
                            // window".
                            //
                            // Two lines cover the bottom border plus one hint
                            // line, which is what these programs draw there.
                            // When there is nothing below — cursor on the
                            // grid's last line — `coerceIn` saturates at
                            // `tapado` and the margin simply costs nothing:
                            // the end of the frame is already on show.
                            // ── AND THE SAME RULE THE OTHER WAY ──────────────
                            //
                            // The remote program's frame is as tall as IT
                            // likes. Claude Code's writing box, with its
                            // footer, takes some twenty lines; the grid has
                            // fifty-two. The remaining thirty-two sat blank
                            // BELOW, and the box stopped in the top third of
                            // the screen — far from the thumb and from the
                            // keyboard. The owner's report: "the writing
                            // window has to always stay at the bottom".
                            //
                            // A desktop terminal never shows this because it
                            // is never taller than the content already
                            // scrolled. Here the grid is born large and filled
                            // by a replay, so the condition exists and the
                            // answer is ours: a single rule, both ways — the
                            // bottom of the CONTENT meets the bottom of the
                            // VISIBLE AREA. See [ScreenAnchor].
                            val board = snapshotState.value
                            val lastWithContent = board?.let { q ->
                                ScreenAnchor.lastRowWithContent(q.cols, q.rows) { x, y ->
                                    val c = q.cellAt(x, y)
                                    c.codepoint == 0 || c.codepoint == SPACE
                                }
                            } ?: -1
                            val lastUseful = ScreenAnchor.lastUsefulRow(
                                lastRowWithContent = lastWithContent,
                                cursorRow = board?.cursorY ?: 0,
                                slackBelowCursor = ROWS_BELOW_CURSOR,
                                lines = board?.rows ?: 1,
                            )
                            placed.place(
                                0,
                                ScreenAnchor.offsetY(
                                    contentBottomPx = (lastUseful + 1) * metrics.cellHeightPx,
                                    visibleHeightPx = constraints.maxHeight,
                                    maxLiftPx = covered,
                                ),
                            )
                        }
                    }
                    .onSizeChanged { size ->
                        // `size` IS ALREADY the full height: the
                        // `Modifier.layout` above measured the box as if the
                        // keyboard were closed. Which is why there is no
                        // correction at all to make here — and that is the
                        // point of the whole fix.
                        val cols = GridGeometry.columns(size.width, metrics.cellWidthPx)
                        val rows = GridGeometry.lines(size.height, metrics.cellHeightPx)
                        gridCols = cols
                        gridRows = rows
                        heightWithoutKeyboardPx = size.height
                        heightForRowsPx = size.height
                        TerminalDiag.log(
                            "MEDIDA cheia=${size.width}x${size.height}px " +
                                "celula=${metrics.cellWidthPx}x${metrics.cellHeightPx} " +
                                "grade=${cols}x$rows",
                        )
                        viewModel.onGridSizeChanged(cols, rows)
                    }
                    .canvasDragGestures(canvasDragTarget)
                    .canvasTapGesture(canvasTapTarget)
                    // LAST in the chain on purpose: the innermost
                    // modifier gets the event first on the Main pass, and that
                    // is how the vertical drag manages to claim the gesture
                    // before the other two recognisers decide. The three still
                    // coexist by giving way — this one only consumes once it
                    // is sure the gesture is its own.
                    .canvasScrollGesture(scrollTarget),
            ) {
                TerminalCanvas(
                    snapshotState = snapshotState,
                    cellWidthPx = metrics.cellWidthPx.toFloat(),
                    cellHeightPx = metrics.cellHeightPx.toFloat(),
                    glyphAtlas = glyphAtlas,
                    modifier = Modifier.fillMaxSize(),
                    // The grid follows the chosen theme. WITHOUT this
                    // line the `TerminalCanvas` falls back to the dark default
                    // and the grid turns black inside a light app — which is
                    // exactly what the emulator capture showed when this line
                    // was lost in a merge. See [currentTerminalPalette] on why
                    // the palette comes from the colour scheme and not from
                    // the system mode.
                    palette = terminalPalette,
                )
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { context ->
                        TerminalInputView(context).apply {
                            byteSink = viewModel.byteSink
                            typingMode = currentTypingMode
                            onCompositionChange = { text -> pendingComposition = text }
                            // Hardware (Bluetooth/USB) keyboard events travel
                            // through `dispatchKeyEvent`/`setOnKeyListener`,
                            // never through `sendKeyEvent` on the
                            // InputConnection this view returns -- so this
                            // listener and TerminalInputConnection's own
                            // IME-commit dedup guard never see the same byte
                            // twice: hardware key -> HardwareKeyHandler
                            // (here), IME commit/synthesized key ->
                            // TerminalInputConnection, with no overlap.
                            setOnKeyListener { _, _, event -> hardwareKeyHandler.onKeyEvent(event) }
                            requestFocus()
                        }.also { view ->
                            inputView.value = view
                            // The floating bar needs a View to host the
                            // `ActionMode`, and this one covers exactly the
                            // grid's area — which is why the selection
                            // rectangle can go straight through, with no
                            // coordinate-space conversion.
                            selectionBar.value = TerminalActionMode(
                                host = view,
                                onCopy = copyAction::copy,
                                onSelectAll = {
                                    viewModel.currentSnapshot()?.let {
                                        selectionController.setSelection(selectAll(it))
                                    }
                                },
                                onPaste = pasteAction::paste,
                                onShare = share,
                                onSendToTerminal = sendToTerminal,
                                onClose = selectionController::clearSelection,
                                otherAppActions = { otherAppActions(view.context) },
                                onUseOtherApp = openInOtherApp,
                                selectionRect = {
                                    selectionHolder.selection
                                        ?.let { selectionBounds(it, hitTesterProvider()) }
                                        ?: Rect.Zero
                                },
                            )
                        }
                    },
                    update = { view ->
                        view.byteSink = viewModel.byteSink
                        // The setter only acts when the value really
                        // changes, and it is what restarts IME input — which
                        // is why a change of mode from the options sheet
                        // reaches a keyboard that is already open, instead of
                        // waiting for it to close and reopen.
                        view.typingMode = currentTypingMode
                    },
                )
                // ABOVE the input view, on purpose: the handles have to
                // get the touch before the grid, and Compose delivers the
                // event to the topmost node first.
                SelectionOverlay(
                    selectionState = selectionState,
                    hitTesterProvider = hitTesterProvider,
                    onDragHandle = selectionController::dragHandle,
                    onHandleDragEnd = { selectionBar.value?.show() },
                    modifier = Modifier.fillMaxSize(),
                )
                // Where they are in the history and how to get back. It
                // only appears once they have left the bottom — pinned at the
                // bottom, the grid stays clean.
                ScrollPositionOverlay(
                    state = scrollState,
                    hasNewOutput = hasNewOutput,
                    onBackToEnd = viewModel::scrollToBottom,
                )
            }
            // Between the grid and the keys: where the thumb already is
            // and where the eye already looks while assembling the command the
            // path will go into. With no attachment it emits no node — 0 dp of
            // cost (see AttachmentBar).
            TerminalAttachment(
                sheetOpen = attachmentOpen,
                onCloseSheet = { attachmentOpen = false },
                // Inserting the path leaves through the SAME sendPaste as
                // a paste: it is what decides, from the remote program's real
                // mode, whether the text goes wrapped in bracketed paste.
                onInsertText = viewModel::sendPaste,
                // With the connection down, `TerminalSocketClient.send`
                // drops the bytes silently — the attachment cannot vanish
                // because of that. See NOTICE_TERMINAL_OFFLINE.
                terminalReady = connectionState == ConnectionState.Live,
            )
            // The word held back by the keyboard's autocorrect, just above
            // the keys and just below the grid — between what has already
            // reached the terminal and what is still being typed, which is
            // where the eye already is.
            //
            // The height is RESERVED in text mode: it was this strip appearing
            // and disappearing that resized the grid on every word (77 resizes
            // in 45 min, measured) and made the screen duplicate. See the
            // strip's own KDoc.
            CompositionStrip(
                text = pendingComposition,
                reserveSpace = currentTypingMode == TypingMode.TEXT,
            )
            ExtraKeysBar(
                pendingModifiers = pendingModifiers,
                onSendBytes = viewModel.byteSink::send,
                state = keysBarState,
                onStateChange = { keysBarState = it },
                hasHardwareKeyboard = hasHardwareKeyboard,
                // The paperclip opens the SAME source sheet the options
                // sheet already opened (`AttachmentSourceSheet`) — file, image
                // or take a photo. What was missing was not the feature: it
                // was a short path to it, from inside the keyboard, which is
                // where attaching is thought of. The upload, the progress and
                // inserting the path all still live in `TerminalAttachment`.
                onAttach = { attachmentOpen = true },
            )
        }

        if (sessionsOpen) {
            SessionSwitcherSheet(
                currentSession = viewModel.sessionName,
                state = sessionsState,
                onSwitchSession = { name ->
                    sessionsOpen = false
                    onSwitchSession(name)
                },
                onCreateSession = { name ->
                    sessionsOpen = false
                    // Creating and attaching are the SAME operation on
                    // this server: the backend opens the session on the first
                    // attach. Reusing the switch path instead of inventing an
                    // endpoint keeps a single way into the terminal.
                    onSwitchSession(name)
                },
                onDetach = {
                    sessionsOpen = false
                    onBack()
                },
                onDismissRequest = { sessionsOpen = false },
            )
        }

        if (optionsOpen) {
            TerminalOptionsSheet(
                fontSizeSp = fontSizeSp,
                onFontSizeChange = { newSize ->
                    coroutineScope.launch { fontSizePreference.setFontSizeSp(newSize) }
                },
                gridCols = gridCols,
                gridRows = gridRows,
                visibleRows = visibleRows,
                onVisibleRowsChange = { next ->
                    coroutineScope.launch { visibleRowsPreference.setRows(next.lines) }
                },
                visibleHeightPx = heightWithoutKeyboardPx,
                referenceHeightPx = heightWithoutKeyboardPx,
                scrollbackLines = scrollbackLines,
                onScrollbackChange = { next ->
                    coroutineScope.launch { scrollbackPreference.setRows(next) }
                },
                typingMode = currentTypingMode,
                onTypingModeChange = { next ->
                    coroutineScope.launch { typingModePreference.setMode(next) }
                },
                lineSpacing = lineSpacing,
                onLineSpacingChange = { next ->
                    coroutineScope.launch { lineSpacingPreference.setLineSpacing(next) }
                },
                onPaste = {
                    optionsOpen = false
                    pasteAction.paste()
                },
                // Close this sheet before opening the source one: two
                // stacked ModalBottomSheets fight over the same window focus.
                onAttach = {
                    optionsOpen = false
                    attachmentOpen = true
                },
                // Ask for the keyboard while the sheet is still up: it is
                // TerminalInputView.showKeyboard() that holds the request back
                // until the sheet's window hands focus over — `showSoftInput`
                // on an unfocused window is silently ignored.
                onShowKeyboard = {
                    optionsOpen = false
                    requestKeyboard()
                },
                batteryExempt = batteryExempt,
                // Explain BEFORE the system asks. A system dialog with no
                // context is denied by reflex — and once denied, it is not
                // offered again on its own.
                onRequestBatteryExemption = {
                    optionsOpen = false
                    exemptionDialogOpen = true
                },
                onDismissRequest = { optionsOpen = false },
            )
        }

        if (exemptionDialogOpen) {
            AlertDialog(
                onDismissRequest = { exemptionDialogOpen = false },
                modifier = Modifier.testTag(BATTERY_EXEMPTION_DIALOG_TAG),
                title = { Text(text = BATTERY_EXEMPTION_LABEL) },
                text = {
                    Text(
                        text = BATTERY_EXEMPTION_EXPLANATION,
                        modifier = Modifier.verticalScroll(rememberScrollState()),
                    )
                },
                confirmButton = {
                    TextButton(
                        onClick = {
                            exemptionDialogOpen = false
                            openBatteryExemptionRequest(context)
                        },
                    ) { Text(text = "Continue") }
                },
                // "Not now" and not "Cancel": refusing here closes no
                // door — the app goes on working the same, just reconnecting
                // more often, and the item stays on the sheet for whenever
                // they want it.
                dismissButton = {
                    TextButton(onClick = { exemptionDialogOpen = false }) {
                        Text(text = "Not now")
                    }
                },
            )
        }
    }
}

/**
 * Converts the chosen font size (in `sp`) into cell dimensions in pixels. The
 * `sp -> px` conversion lives here because only this layer has the [Density];
 * the measurement itself lives in `computeTerminalCellMetrics`, next to the
 * [GlyphAtlas] that rasterises with the SAME typeface and the SAME `textSize`
 * — the grid this screen places and the glyphs the atlas draws have to agree
 * on scale, and that is only guaranteed by measuring in one single place.
 */
private fun computeCellMetrics(
    density: Density,
    fontSizeSp: Float,
    lineSpacing: TerminalLineSpacing,
): TerminalCellMetrics = computeTerminalCellMetrics(
    fontSizePx = with(density) { fontSizeSp.sp.toPx() },
    lineSpacingPx = lineSpacing.deltaPx,
)

/**
 * How many lines to keep visible BELOW the cursor when the keyboard comes up.
 *
 * It is not aesthetic slack: it is the bottom border of the input box of the
 * programs used here. With zero, the keyboard cut off exactly that line.
 */
private const val ROWS_BELOW_CURSOR = 2

/** The space codepoint — a cell holding it draws nothing. */
private const val SPACE = 32

/**
 * The largest type size that still makes [lines] lines fit in [heightPx].
 *
 * ## Why a search and not a division
 *
 * Cell height is not a linear function of the font size: it goes through
 * rounding to a whole pixel (the condition for the renderer's 1:1 blit) and
 * adds the line spacing as an integer delta. Inverting that analytically would
 * give a formula that is off by a pixel now and then — and one extra pixel per
 * line, over 45 lines, is a whole line lost.
 *
 * The search costs a few dozen text measurements, once per change of size or
 * preference, and is right by construction: it returns the first size, from
 * largest to smallest, whose grid REALLY fits.
 *
 * The 6 sp floor exists for the degenerate case (a tiny window, 60 lines asked
 * for): better to hand back small, illegible type than a division by zero or a
 * one-line grid.
 */
private fun bodyThatFits(
    lines: Int,
    heightPx: Int,
    density: Density,
    lineSpacing: TerminalLineSpacing,
): TerminalCellMetrics {
    var last = computeCellMetrics(density, MIN_BODY_SP, lineSpacing)
    var body = MAX_BODY_SP
    while (body >= MIN_BODY_SP) {
        val m = computeCellMetrics(density, body, lineSpacing)
        if (m.cellHeightPx * lines <= heightPx) return m
        last = m
        body -= SEARCH_STEP_SP
    }
    return last
}

private const val MAX_BODY_SP = 28f
private const val MIN_BODY_SP = 6f
private const val SEARCH_STEP_SP = 0.5f
