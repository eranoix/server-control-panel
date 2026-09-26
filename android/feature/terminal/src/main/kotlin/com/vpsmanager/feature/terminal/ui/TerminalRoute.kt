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
 * Screen-reader description of the back arrow. Only this detail bar has one (top
 * level destinations show the shell's menu button there), so the shell test reads
 * it from here to tell the headers apart.
 */
const val BACK_DESCRIPTION = "Back"

/** Test tag for the session button in the top bar. */
const val SESSIONS_TAG = "sessoes-terminal"

/** What the session button says in the top bar. */
const val LABEL_SESSIONS = "Session"

/**
 * Shown when "Paste" is tapped with nothing copied. The item is always on the
 * toolbar, so it must say why nothing happened; see `PasteAction`.
 */
const val NOTICE_NOTHING_TO_PASTE = "There is nothing copied to paste"


/**
 * The live terminal screen. [TerminalViewModel] is built from
 * `createSavedStateHandle()` so a relaunch after process death restores the same
 * session name (see the ViewModel's KDoc).
 *
 * The column is deliberately short: top bar, grid, key bar. Episodic controls
 * live in [TerminalOptionsSheet] (0 dp while closed), the connection banner only
 * appears when it has something to say, the key bar's toggle is a handle inside
 * [ExtraKeysBar], and copy/paste use the system floating toolbar
 * ([TerminalActionMode]).
 *
 * The grid uses `weight(1f)`, not `fillMaxSize()`: inside a `Column` the latter
 * takes all remaining space and leaves the key bar with zero height.
 *
 * No `imePadding()` or `consumeWindowInsets` here: `AppNavHost`'s `NavHost`
 * applies both once for every destination, and a second consumer would apply the
 * inset twice.
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
    // The banner reads the delayed state (1.5 s grace); the logic reads the real
    // state above. A reconnect that settles in about 1 s never lights the banner.
    val bannerState by viewModel.bannerState.collectAsStateWithLifecycle()
    val bridgeOrigin by viewModel.bridgeCommandOrigin.collectAsStateWithLifecycle()
    val pendingTyping by viewModel.pendingTyping.collectAsStateWithLifecycle()
    val typingDiscarded by viewModel.typingDiscarded.collectAsStateWithLifecycle()
    val isStalled by viewModel.isStalled.collectAsStateWithLifecycle()

    val density = LocalDensity.current
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()

    var sessionsOpen by remember { mutableStateOf(false) }
    // Sessions come from the same source as the list screen
    // ([SessionListViewModel]); this is a second view, not a second truth.
    val sessionsViewModel: SessionListViewModel = viewModel()
    val sessionsState by sessionsViewModel.uiState.collectAsStateWithLifecycle()

    var batteryExempt by remember { mutableStateOf(isBatteryOptimizationExempt(context)) }
    var exemptionDialogOpen by remember { mutableStateOf(false) }

    // One observer, two events:
    // ON_START: reconnect. Android 15+ kills background sockets within seconds,
    //   so on return the connection is always dead; ON_START only fires when the
    //   screen was really invisible.
    // ON_RESUME: re-read the battery exemption. The system dialog that grants it
    //   is translucent and never stops this screen, so ON_START would not fire.
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
    // Font size, stored on the device only (see TerminalFontSizePreference);
    // a change rebuilds the GlyphAtlas and cell metrics below.
    val fontSizePreference = remember { TerminalFontSizePreference(context) }
    val fontSizeSp by fontSizePreference.fontSizeSp.collectAsStateWithLifecycle(
        initialValue = TerminalFontSizePreference.DEFAULT_FONT_SIZE_SP,
    )
    // Line spacing: also device-only. It changes the cell height, hence the row
    // count, hence the resize sent to the server.
    val lineSpacingPreference = remember { TerminalLineSpacingPreference(context) }
    val lineSpacing by lineSpacingPreference.lineSpacing.collectAsStateWithLifecycle(
        initialValue = TerminalLineSpacing.DEFAULT,
    )
    // Typing mode: changes the view's `inputType`, so a switch restarts IME
    // input (see TerminalInputView.typingMode). It does not affect metrics.
    // Scrollback: handed to the ViewModel, which uses it when creating the engine
    // and to size the history fetch. `byRows` snaps values from older ladders to
    // a current step.
    val scrollbackPreference = remember { TerminalScrollbackPreference(context) }
    val savedScrollback by scrollbackPreference.lines.collectAsStateWithLifecycle(
        initialValue = TerminalScrollback.DEFAULT.lines,
    )
    val scrollbackLines = TerminalScrollback.byRows(savedScrollback).lines
    // Arrives from DataStore after the ViewModel is built; the engine is only
    // created once the grid is measured, so this is in time.
    viewModel.scrollbackLines = scrollbackLines
    // Rows on screen: when pinned, the font size is derived from it. See [VisibleRows].
    val visibleRowsPreference = remember { VisibleRowsPreference(context) }
    val visibleRowsValue by visibleRowsPreference.lines.collectAsStateWithLifecycle(
        initialValue = VisibleRows.DEFAULT.lines,
    )
    val visibleRows = VisibleRows.byRows(visibleRowsValue)

    val typingModePreference = remember { TypingModePreference(context) }
    val currentTypingMode by typingModePreference.mode.collectAsStateWithLifecycle(
        initialValue = TypingMode.DEFAULT,
    )
    // The word the keyboard is composing (TEXT mode only).
    var pendingComposition by remember { mutableStateOf("") }
    // Theme colours for the grid, read here so a theme change repaints the grid
    // in the same recomposition as the rest.
    val terminalPalette = currentTerminalPalette
    // Available height, used to derive the font size when the row count is
    // pinned. Zero until the first measurement; the chosen size is used meanwhile.
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
    // The atlas is rebuilt, not resized, on every metrics change; recycle the old
    // bitmaps so memory stays at one atlas.
    DisposableEffect(glyphAtlas) {
        onDispose {
            glyphAtlas.narrowBitmap.recycle()
            glyphAtlas.wideBitmap.recycle()
        }
    }
    val snapshotState = remember { mutableStateOf<CellSnapshot?>(null) }
    // Sticky Ctrl/Alt shared between the on-screen row and HardwareKeyHandler, so
    // an armed modifier applies to both.
    val pendingModifiers = remember { PendingModifiers() }
    val hardwareKeyHandler = remember(viewModel) {
        HardwareKeyHandler(viewModel.byteSink, pendingModifiers = pendingModifiers)
    }

    // Selection is its own holder plus polled state, not ViewModel state: it is
    // pure canvas gesture UI, never written from IME code (see GridSelection and
    // SelectionComposeIndependenceTest).
    val selectionHolder = remember { GridSelectionHolder() }
    val selectionState = remember { mutableStateOf<GridSelection?>(null) }
    var gridCols by remember { mutableStateOf(1) }
    var gridRows by remember { mutableStateOf(1) }

    // Insets read as objects and queried inside `Modifier.layout`: reading them
    // during measurement is a layout-phase read, so the grid remeasures while the
    // keyboard slides without recomposing this function (as `imePadding()` does).
    val keyboardInsets = WindowInsets.ime
    val navBarInsets = WindowInsets.navigationBars

    /** The grid height with the keyboard closed; for the options sheet only. */
    var heightWithoutKeyboardPx by remember { mutableStateOf(0) }

    val hitTesterProvider: () -> CellHitTester = remember(metrics) {
        {
            CellHitTester(
                cellWidthPx = metrics.cellWidthPx.toFloat(),
                cellHeightPx = metrics.cellHeightPx.toFloat(),
                cols = gridCols,
                rows = gridRows,
                // No `originY`: the grid is offset during placement, so pointer
                // coordinates already include it. See the grid's `Modifier.layout`.
            )
        }
    }

    // Modes the remote program enabled, re-read every frame: programs toggle mouse
    // reporting without telling anyone.
    var terminalModes by remember { mutableStateOf(TerminalModes.NONE) }

    // The system floating toolbar, created with the `TerminalInputView` that hosts
    // its `ActionMode` (in the `AndroidView` below).
    val selectionBar = remember { mutableStateOf<TerminalActionMode?>(null) }

    val selectionController = remember(hitTesterProvider, selectionHolder) {
        SelectionGestureController(hitTesterProvider, selectionHolder) { selection ->
            // Nothing observes the holder; the toolbar must be called explicitly.
            val bar = selectionBar.value
            if (selection == null) bar?.hide() else bar?.show()
        }
    }

    // No preference: each gesture's owner follows the mode the remote program
    // enabled. See [TouchRouting].
    val touchRouting = remember(viewModel) {
        TouchRouting { viewModel.currentModes().mouseTracking }
    }

    // The VT emulator does the encoding (it knows the mode and format); this only
    // supplies the grid geometry.
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

    // Vertical drag scrolls history; where it scrolls follows the emulator's real
    // state, as with mouse and paste. See [decideScroll].
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
            // Whether there is room left, so the fling stops at the top of the history.
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
    // Held so the keyboard can be requested from outside (grid tap, options
    // sheet); the request must come from the view that owns the InputConnection.
    // See TerminalInputView.showKeyboard().
    val inputView = remember { mutableStateOf<TerminalInputView?>(null) }
    val requestKeyboard: () -> Unit = { inputView.value?.showKeyboard() }

    /**
     * A grid tap when the app owns the gesture: one tap raises the keyboard, two
     * select the word, three the logical line, as in any Android text field.
     */
    val tapKeyboardTarget = remember(selectionController, viewModel, hitTesterProvider) {
        CanvasTapTarget { position, taps ->
            val snapshot = viewModel.currentSnapshot()
            if (snapshot == null || taps < DOUBLE_TAP) {
                selectionController.clearSelection()
                inputView.value?.showKeyboard()
                return@CanvasTapTarget
            }
            // Grid and snapshot may disagree for a frame during a resize; clamp
            // to the snapshot, which is the source of the text.
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
            // "Paste" is always on the toolbar, so it may have nothing to do. A
            // Toast, not a Snackbar: the toolbar is a separate window above, and a
            // Snackbar would appear behind it.
            onContentMissing = {
                Toast.makeText(context, NOTICE_NOTHING_TO_PASTE, Toast.LENGTH_SHORT).show()
            },
        )
    }
    // The selected text, read at click time, for the overflow actions that
    // bypass the clipboard.
    val selectedText = remember(selectionHolder, viewModel) {
        SelectedText(
            snapshotProvider = viewModel::currentSnapshot,
            selectionProvider = { selectionHolder.selection },
        )
    }
    val share: () -> Unit = remember(selectedText, context) {
        { selectedText.use { text -> shareText(context, text) } }
    }
    // Sending the selection back goes through the same `sendPaste`, so the
    // emulator decides on bracketed-paste wrapping (see `PasteAction`).
    val sendToTerminal: () -> Unit = remember(selectedText, viewModel) {
        { selectedText.use(viewModel::sendPaste) }
    }
    // Other apps' text actions ("Translate", "Search") are enumerated by the
    // toolbar when it opens, so app installs are reflected without recreation.
    val openInOtherApp = remember(selectedText, context) {
        { action: OtherAppAction ->
            selectedText.use { text ->
                context.startActivity(processTextIntent(action, text))
            }
        }
    }

    // A physical keyboard is attached (`HARDKEYBOARDHIDDEN_NO`); updates live when
    // a Bluetooth keyboard connects or disconnects.
    val configuration = LocalConfiguration.current
    val hasHardwareKeyboard = configuration.hardKeyboardHidden == Configuration.HARDKEYBOARDHIDDEN_NO
    var keysBarState by remember { mutableStateOf(ExtraKeysBarState.ONE_ROW) }
    var optionsOpen by remember { mutableStateOf(false) }
    // Only the attachment sheet toggle lives here; upload, progress and path
    // insertion live in TerminalAttachment.
    var attachmentOpen by remember { mutableStateOf(false) }

    // Renderer polling, decoupled from byte arrival so redraws follow the frame
    // rate, not server write volume. Selection and scroll position are polled in
    // the same frame (libghostty-vt does not notify scroll changes).
    var scrollState by remember { mutableStateOf(TerminalScrollState.AT_END) }
    // History size when the user left the bottom; growth after that means new
    // output arrived off screen, which the UI must announce.
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
                    // Back at the bottom: nothing new is out of sight.
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
                // Half the default height (64 dp): every dp is another line of
                // output. The font size is unchanged.
                expandedHeight = 32.dp,
                title = { Text(text = viewModel.sessionName) },
                // A detail screen, so a real back arrow to the list.
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = BACK_DESCRIPTION,
                        )
                    }
                },
                actions = {
                    // Session switcher: switching here keeps the grid, while going
                    // back to the list would drop it and redo the attach.
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
                    // The terminal background paints the whole area, since the
                    // grid may be placed below the top (see ScreenAnchor).
                    .background(Color(0xFF000000 or terminalPalette.defaultBg.toLong()))
                    // Clip before `layout`, so the part lifted upwards does not
                    // paint over the top bar.
                    .clipToBounds()
                    // The grid does not shrink with the keyboard: it is measured at
                    // the keyboard-closed height and placed shifted up. The row
                    // count stays constant, which avoids resize-driven duplication
                    // (see GridGeometry). Shifting during placement keeps touch,
                    // selection and the input view aligned without corrections.
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
                            // Offset: the minimum that keeps the cursor visible,
                            // plus [ROWS_BELOW_CURSOR] for TUI content below it (an
                            // input box's bottom border). Anchoring to the bottom
                            // instead would show blank rows of a fresh session.
                            // Conversely, a frame shorter than the grid is moved
                            // down so the content bottom meets the visible bottom.
                            // See [ScreenAnchor]. The snapshot is read during
                            // placement, so cursor moves only re-place the grid.
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
                        // `size` is already the full height: the `layout` above
                        // measured as if the keyboard were closed.
                        val cols = GridGeometry.columns(size.width, metrics.cellWidthPx)
                        val rows = GridGeometry.lines(size.height, metrics.cellHeightPx)
                        gridCols = cols
                        gridRows = rows
                        heightWithoutKeyboardPx = size.height
                        heightForRowsPx = size.height
                        TerminalDiag.log(
                            "MEASURED full=${size.width}x${size.height}px " +
                                "cell=${metrics.cellWidthPx}x${metrics.cellHeightPx} " +
                                "grid=${cols}x$rows",
                        )
                        viewModel.onGridSizeChanged(cols, rows)
                    }
                    .canvasDragGestures(canvasDragTarget)
                    .canvasTapGesture(canvasTapTarget)
                    // Last in the chain on purpose: the innermost modifier sees the
                    // event first, so the vertical drag can claim it before the
                    // others decide; it only consumes once sure.
                    .canvasScrollGesture(scrollTarget),
            ) {
                TerminalCanvas(
                    snapshotState = snapshotState,
                    cellWidthPx = metrics.cellWidthPx.toFloat(),
                    cellHeightPx = metrics.cellHeightPx.toFloat(),
                    glyphAtlas = glyphAtlas,
                    modifier = Modifier.fillMaxSize(),
                    // Follow the chosen theme; without this the canvas falls back
                    // to the dark palette. See [currentTerminalPalette].
                    palette = terminalPalette,
                )
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { context ->
                        TerminalInputView(context).apply {
                            byteSink = viewModel.byteSink
                            typingMode = currentTypingMode
                            onCompositionChange = { text -> pendingComposition = text }
                            // Hardware keys arrive via `setOnKeyListener`, never
                            // through the InputConnection's `sendKeyEvent`, so this
                            // handler and the IME dedup guard never see the same byte.
                            setOnKeyListener { _, _, event -> hardwareKeyHandler.onKeyEvent(event) }
                            requestFocus()
                        }.also { view ->
                            inputView.value = view
                            // The view covers exactly the grid, so the selection
                            // rectangle needs no coordinate conversion.
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
                        // The setter acts only on change and restarts IME input,
                        // so a mode switch reaches an already open keyboard.
                        view.typingMode = currentTypingMode
                    },
                )
                // Above the input view: handles must get the touch first, and
                // Compose delivers to the topmost node first.
                SelectionOverlay(
                    selectionState = selectionState,
                    hitTesterProvider = hitTesterProvider,
                    onDragHandle = selectionController::dragHandle,
                    onHandleDragEnd = { selectionBar.value?.show() },
                    modifier = Modifier.fillMaxSize(),
                )
                // Position in history and the way back; only after leaving the bottom.
                ScrollPositionOverlay(
                    state = scrollState,
                    hasNewOutput = hasNewOutput,
                    onBackToEnd = viewModel::scrollToBottom,
                )
            }
            // Between the grid and the keys, where thumb and eye already are. No
            // node when there is no attachment (see AttachmentBar).
            TerminalAttachment(
                sheetOpen = attachmentOpen,
                onCloseSheet = { attachmentOpen = false },
                // Inserting the path uses the same `sendPaste`, so bracketed-paste
                // wrapping follows the program's mode.
                onInsertText = viewModel::sendPaste,
                // While offline, `TerminalSocketClient.send` may drop bytes, so
                // insertion waits for the connection. See NOTICE_TERMINAL_OFFLINE.
                terminalReady = connectionState == ConnectionState.Live,
            )
            // The word held by autocorrect, between grid and keys. Its height is
            // reserved in TEXT mode, since toggling it resized the grid on every
            // word. See the strip's KDoc.
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
                // The paperclip opens the same source sheet as the options sheet,
                // a shorter path from the keyboard. Upload and insertion stay in
                // `TerminalAttachment`.
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
                    // Creating and attaching are the same operation: the backend
                    // opens the session on first attach.
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
                // Close this sheet first: two stacked ModalBottomSheets fight over
                // window focus.
                onAttach = {
                    optionsOpen = false
                    attachmentOpen = true
                },
                // Request the keyboard while the sheet is still up;
                // showKeyboard() waits for window focus, since `showSoftInput` on
                // an unfocused window is silently ignored.
                onShowKeyboard = {
                    optionsOpen = false
                    requestKeyboard()
                },
                batteryExempt = batteryExempt,
                // Explain before the system asks: an unexplained system dialog is
                // denied by reflex and not offered again.
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
                // "Not now", not "Cancel": declining closes no door, and the item
                // stays in the sheet.
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
 * Converts the font size in `sp` to cell dimensions in pixels. Only this layer has
 * the [Density]; the measurement itself is in `computeTerminalCellMetrics`, next to
 * [GlyphAtlas], so grid and glyphs agree on scale.
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
 * Lines kept visible below the cursor when the keyboard is up: the bottom border
 * of TUI input boxes, which the keyboard would otherwise cover.
 */
private const val ROWS_BELOW_CURSOR = 2

/** The space codepoint; a cell holding it draws nothing. */
private const val SPACE = 32

/**
 * The largest font size that fits [lines] lines in [heightPx]. A search rather
 * than a division: cell height involves whole-pixel rounding and an integer
 * spacing delta, so an analytic inverse would sometimes be off by a pixel per
 * line. Runs once per size or preference change. The 6 sp floor handles
 * degenerate cases (tiny window, many lines).
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
