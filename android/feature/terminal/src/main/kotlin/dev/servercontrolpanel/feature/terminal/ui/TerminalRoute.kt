package dev.servercontrolpanel.feature.terminal.ui

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
import dev.servercontrolpanel.terminalengine.TerminalScrollState
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
import dev.servercontrolpanel.feature.terminal.attach.TerminalAttachment
import dev.servercontrolpanel.feature.terminal.geometry.ScreenAnchor
import dev.servercontrolpanel.feature.terminal.geometry.GridGeometry
import dev.servercontrolpanel.feature.terminal.input.TerminalInputView
import dev.servercontrolpanel.feature.terminal.transport.ConnectionState
import dev.servercontrolpanel.feature.terminal.keys.ExtraKeysBar
import dev.servercontrolpanel.feature.terminal.keys.ExtraKeysBarState
import dev.servercontrolpanel.feature.terminal.keys.HardwareKeyHandler
import dev.servercontrolpanel.feature.terminal.keys.PendingModifiers
import dev.servercontrolpanel.feature.terminal.mouse.MouseEventEncoder
import dev.servercontrolpanel.feature.terminal.mouse.MouseReportGestureController
import dev.servercontrolpanel.feature.terminal.mouse.TouchRouting
import dev.servercontrolpanel.feature.terminal.power.BATTERY_EXEMPTION_DIALOG_TAG
import dev.servercontrolpanel.feature.terminal.power.BATTERY_EXEMPTION_EXPLANATION
import dev.servercontrolpanel.feature.terminal.power.BATTERY_EXEMPTION_LABEL
import dev.servercontrolpanel.feature.terminal.power.openBatteryExemptionRequest
import dev.servercontrolpanel.feature.terminal.power.isBatteryOptimizationExempt
import dev.servercontrolpanel.feature.terminal.prefs.TerminalFontSizePreference
import dev.servercontrolpanel.feature.terminal.prefs.VisibleRows
import dev.servercontrolpanel.feature.terminal.prefs.VisibleRowsPreference
import dev.servercontrolpanel.feature.terminal.prefs.TypingMode
import dev.servercontrolpanel.feature.terminal.prefs.TypingModePreference
import dev.servercontrolpanel.feature.terminal.prefs.TerminalLineSpacing
import dev.servercontrolpanel.feature.terminal.prefs.TerminalScrollback
import dev.servercontrolpanel.feature.terminal.prefs.TerminalScrollbackPreference
import dev.servercontrolpanel.feature.terminal.prefs.TerminalLineSpacingPreference
import dev.servercontrolpanel.feature.terminal.render.GlyphAtlas
import dev.servercontrolpanel.feature.terminal.render.TerminalCanvas
import dev.servercontrolpanel.feature.terminal.render.TerminalCellMetrics
import dev.servercontrolpanel.feature.terminal.render.computeTerminalCellMetrics
import dev.servercontrolpanel.feature.terminal.render.currentTerminalPalette
import dev.servercontrolpanel.feature.terminal.scroll.ScrollPositionOverlay
import dev.servercontrolpanel.feature.terminal.scroll.ScrollbackGestureController
import dev.servercontrolpanel.feature.terminal.scroll.canvasScrollGesture
import dev.servercontrolpanel.feature.terminal.selection.OtherAppAction
import dev.servercontrolpanel.feature.terminal.transport.TerminalDiag
import dev.servercontrolpanel.feature.terminal.selection.CanvasTapTarget
import dev.servercontrolpanel.feature.terminal.selection.CellHitTester
import dev.servercontrolpanel.feature.terminal.selection.CopyAction
import dev.servercontrolpanel.feature.terminal.selection.GridSelection
import dev.servercontrolpanel.feature.terminal.selection.GridSelectionHolder
import dev.servercontrolpanel.feature.terminal.selection.PasteAction
import dev.servercontrolpanel.feature.terminal.selection.SelectionGestureController
import dev.servercontrolpanel.feature.terminal.selection.SelectionOverlay
import dev.servercontrolpanel.feature.terminal.selection.DOUBLE_TAP
import dev.servercontrolpanel.feature.terminal.selection.TRIPLE_TAP
import dev.servercontrolpanel.feature.terminal.selection.TerminalActionMode
import dev.servercontrolpanel.feature.terminal.selection.SelectedText
import dev.servercontrolpanel.feature.terminal.selection.otherAppActions
import dev.servercontrolpanel.feature.terminal.selection.canvasDragGestures
import dev.servercontrolpanel.feature.terminal.selection.canvasTapGesture
import dev.servercontrolpanel.feature.terminal.selection.shareText
import dev.servercontrolpanel.feature.terminal.selection.processTextIntent
import dev.servercontrolpanel.feature.terminal.selection.routeCanvasDrag
import dev.servercontrolpanel.feature.terminal.selection.routeCanvasTap
import dev.servercontrolpanel.feature.terminal.selection.selectionBounds
import dev.servercontrolpanel.feature.terminal.selection.selectLine
import dev.servercontrolpanel.feature.terminal.selection.selectWord
import dev.servercontrolpanel.feature.terminal.selection.selectAll
import dev.servercontrolpanel.terminalengine.CellSnapshot
import dev.servercontrolpanel.terminalengine.MouseGeometry
import dev.servercontrolpanel.terminalengine.TerminalModes
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

const val BACK_DESCRIPTION = "Back"

const val SESSIONS_TAG = "sessions-terminal"

const val LABEL_SESSIONS = "Session"

const val NOTICE_NOTHING_TO_PASTE = "There is nothing copied to paste"


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
    val bannerState by viewModel.bannerState.collectAsStateWithLifecycle()
    val bridgeOrigin by viewModel.bridgeCommandOrigin.collectAsStateWithLifecycle()
    val pendingTyping by viewModel.pendingTyping.collectAsStateWithLifecycle()
    val typingDiscarded by viewModel.typingDiscarded.collectAsStateWithLifecycle()
    val isStalled by viewModel.isStalled.collectAsStateWithLifecycle()

    val density = LocalDensity.current
    val context = LocalContext.current
    val coroutineScope = rememberCoroutineScope()

    var sessionsOpen by remember { mutableStateOf(false) }
    val sessionsViewModel: SessionListViewModel = viewModel()
    val sessionsState by sessionsViewModel.uiState.collectAsStateWithLifecycle()

    var batteryExempt by remember { mutableStateOf(isBatteryOptimizationExempt(context)) }
    var exemptionDialogOpen by remember { mutableStateOf(false) }

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
    val fontSizePreference = remember { TerminalFontSizePreference(context) }
    val fontSizeSp by fontSizePreference.fontSizeSp.collectAsStateWithLifecycle(
        initialValue = TerminalFontSizePreference.DEFAULT_FONT_SIZE_SP,
    )
    val lineSpacingPreference = remember { TerminalLineSpacingPreference(context) }
    val lineSpacing by lineSpacingPreference.lineSpacing.collectAsStateWithLifecycle(
        initialValue = TerminalLineSpacing.DEFAULT,
    )
    val scrollbackPreference = remember { TerminalScrollbackPreference(context) }
    val savedScrollback by scrollbackPreference.lines.collectAsStateWithLifecycle(
        initialValue = TerminalScrollback.DEFAULT.lines,
    )
    val scrollbackLines = TerminalScrollback.byRows(savedScrollback).lines
    viewModel.scrollbackLines = scrollbackLines
    val visibleRowsPreference = remember { VisibleRowsPreference(context) }
    val visibleRowsValue by visibleRowsPreference.lines.collectAsStateWithLifecycle(
        initialValue = VisibleRows.DEFAULT.lines,
    )
    val visibleRows = VisibleRows.byRows(visibleRowsValue)

    val typingModePreference = remember { TypingModePreference(context) }
    val currentTypingMode by typingModePreference.mode.collectAsStateWithLifecycle(
        initialValue = TypingMode.DEFAULT,
    )
    var pendingComposition by remember { mutableStateOf("") }
    val terminalPalette = currentTerminalPalette
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
    DisposableEffect(glyphAtlas) {
        onDispose {
            glyphAtlas.narrowBitmap.recycle()
            glyphAtlas.wideBitmap.recycle()
        }
    }
    val snapshotState = remember { mutableStateOf<CellSnapshot?>(null) }
    val pendingModifiers = remember { PendingModifiers() }
    val hardwareKeyHandler = remember(viewModel) {
        HardwareKeyHandler(viewModel.byteSink, pendingModifiers = pendingModifiers)
    }

    val selectionHolder = remember { GridSelectionHolder() }
    val selectionState = remember { mutableStateOf<GridSelection?>(null) }
    var gridCols by remember { mutableStateOf(1) }
    var gridRows by remember { mutableStateOf(1) }

    val keyboardInsets = WindowInsets.ime
    val navBarInsets = WindowInsets.navigationBars

    var heightWithoutKeyboardPx by remember { mutableStateOf(0) }

    val hitTesterProvider: () -> CellHitTester = remember(metrics) {
        {
            CellHitTester(
                cellWidthPx = metrics.cellWidthPx.toFloat(),
                cellHeightPx = metrics.cellHeightPx.toFloat(),
                cols = gridCols,
                rows = gridRows,
            )
        }
    }

    var terminalModes by remember { mutableStateOf(TerminalModes.NONE) }

    val selectionBar = remember { mutableStateOf<TerminalActionMode?>(null) }

    val selectionController = remember(hitTesterProvider, selectionHolder) {
        SelectionGestureController(hitTesterProvider, selectionHolder) { selection ->
            val bar = selectionBar.value
            if (selection == null) bar?.hide() else bar?.show()
        }
    }

    val touchRouting = remember(viewModel) {
        TouchRouting { viewModel.currentModes().mouseTracking }
    }

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
    val inputView = remember { mutableStateOf<TerminalInputView?>(null) }
    val requestKeyboard: () -> Unit = { inputView.value?.showKeyboard() }

    val tapKeyboardTarget = remember(selectionController, viewModel, hitTesterProvider) {
        CanvasTapTarget { position, taps ->
            val snapshot = viewModel.currentSnapshot()
            if (snapshot == null || taps < DOUBLE_TAP) {
                selectionController.clearSelection()
                inputView.value?.showKeyboard()
                return@CanvasTapTarget
            }
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
            onContentMissing = {
                Toast.makeText(context, NOTICE_NOTHING_TO_PASTE, Toast.LENGTH_SHORT).show()
            },
        )
    }
    val selectedText = remember(selectionHolder, viewModel) {
        SelectedText(
            snapshotProvider = viewModel::currentSnapshot,
            selectionProvider = { selectionHolder.selection },
        )
    }
    val share: () -> Unit = remember(selectedText, context) {
        { selectedText.use { text -> shareText(context, text) } }
    }
    val sendToTerminal: () -> Unit = remember(selectedText, viewModel) {
        { selectedText.use(viewModel::sendPaste) }
    }
    val openInOtherApp = remember(selectedText, context) {
        { action: OtherAppAction ->
            selectedText.use { text ->
                context.startActivity(processTextIntent(action, text))
            }
        }
    }

    val configuration = LocalConfiguration.current
    val hasHardwareKeyboard = configuration.hardKeyboardHidden == Configuration.HARDKEYBOARDHIDDEN_NO
    var keysBarState by remember { mutableStateOf(ExtraKeysBarState.ONE_ROW) }
    var optionsOpen by remember { mutableStateOf(false) }
    var attachmentOpen by remember { mutableStateOf(false) }

    var scrollState by remember { mutableStateOf(TerminalScrollState.AT_END) }
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
                expandedHeight = 32.dp,
                title = { Text(text = viewModel.sessionName) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = BACK_DESCRIPTION,
                        )
                    }
                },
                actions = {
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
                    .background(Color(0xFF000000 or terminalPalette.defaultBg.toLong()))
                    .clipToBounds()
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
                    .canvasScrollGesture(scrollTarget),
            ) {
                TerminalCanvas(
                    snapshotState = snapshotState,
                    cellWidthPx = metrics.cellWidthPx.toFloat(),
                    cellHeightPx = metrics.cellHeightPx.toFloat(),
                    glyphAtlas = glyphAtlas,
                    modifier = Modifier.fillMaxSize(),
                    palette = terminalPalette,
                )
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { context ->
                        TerminalInputView(context).apply {
                            byteSink = viewModel.byteSink
                            typingMode = currentTypingMode
                            onCompositionChange = { text -> pendingComposition = text }
                            setOnKeyListener { _, _, event -> hardwareKeyHandler.onKeyEvent(event) }
                            requestFocus()
                        }.also { view ->
                            inputView.value = view
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
                        view.typingMode = currentTypingMode
                    },
                )
                SelectionOverlay(
                    selectionState = selectionState,
                    hitTesterProvider = hitTesterProvider,
                    onDragHandle = selectionController::dragHandle,
                    onHandleDragEnd = { selectionBar.value?.show() },
                    modifier = Modifier.fillMaxSize(),
                )
                ScrollPositionOverlay(
                    state = scrollState,
                    hasNewOutput = hasNewOutput,
                    onBackToEnd = viewModel::scrollToBottom,
                )
            }
            TerminalAttachment(
                sheetOpen = attachmentOpen,
                onCloseSheet = { attachmentOpen = false },
                onInsertText = viewModel::sendPaste,
                terminalReady = connectionState == ConnectionState.Live,
            )
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
                onAttach = {
                    optionsOpen = false
                    attachmentOpen = true
                },
                onShowKeyboard = {
                    optionsOpen = false
                    requestKeyboard()
                },
                batteryExempt = batteryExempt,
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
                dismissButton = {
                    TextButton(onClick = { exemptionDialogOpen = false }) {
                        Text(text = "Not now")
                    }
                },
            )
        }
    }
}

private fun computeCellMetrics(
    density: Density,
    fontSizeSp: Float,
    lineSpacing: TerminalLineSpacing,
): TerminalCellMetrics = computeTerminalCellMetrics(
    fontSizePx = with(density) { fontSizeSp.sp.toPx() },
    lineSpacingPx = lineSpacing.deltaPx,
)

private const val ROWS_BELOW_CURSOR = 2

private const val SPACE = 32

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
