package com.vpsmanager.feature.terminal.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.feature.terminal.attach.ATTACH_LABEL
import com.vpsmanager.feature.terminal.attach.ATTACH_TAG
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacing
import com.vpsmanager.feature.terminal.power.BATTERY_EXEMPTION_LABEL
import com.vpsmanager.feature.terminal.power.BATTERY_EXEMPTION_TAG
import com.vpsmanager.feature.terminal.prefs.VisibleRows
import com.vpsmanager.feature.terminal.prefs.TypingMode
import com.vpsmanager.feature.terminal.prefs.TerminalScrollback
import com.vpsmanager.feature.terminal.prefs.TerminalFontSizePreference

/** Test tag for the options sheet, which must cost no height while closed. */
const val OPTIONS_SHEET_TAG = "folha-opcoes-terminal"

/** Label of the button that opens the sheet, on the top bar. */
const val OPTIONS_DESCRIPTION = "Terminal options"

/** Label of the button that raises the software keyboard without relying on a tap on the grid. */
const val SHOW_KEYBOARD_LABEL = "Show keyboard"

/** Test tag for the sheet's keyboard button. */
const val SHOW_KEYBOARD_TAG = "botao-mostrar-teclado"

/** Label of the sheet's paste button, the paste path that needs no selection. */
const val PASTE_LABEL = "Paste"

/** Test tag for the sheet's paste button. */
const val PASTE_TAG = "botao-colar"

// There is deliberately no "clear history" button: erasing the user's history
// is never an acceptable way out of a display defect.
/** Test tag for each line-spacing step. */
fun lineSpacingTag(step: TerminalLineSpacing): String = "entrelinha-${step.name}"

/** Tag for each input mode's chip, for the test that proves the switch. */
fun typingModeTag(mode: TypingMode): String = "modo-digitacao-${mode.name}"

/** Tag for each history-size chip. */
fun scrollbackTag(step: TerminalScrollback): String = "scrollback-${step.name}"


/**
 * A section header: icon and title on one line, a short caption below. The icon
 * gives the category at a glance, keeping explanations short enough for the whole
 * sheet to fit on one screen.
 */
@Composable
private fun SectionHeader(icon: ImageVector, title: String, caption: String? = null) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                modifier = Modifier.size(18.dp),
                tint = MaterialTheme.colorScheme.primary,
            )
            Spacer(modifier = Modifier.width(8.dp))
            Text(text = title, style = MaterialTheme.typography.titleSmall)
        }
        if (caption != null) {
            Text(
                text = caption,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * The chips of one choice, in a `FlowRow`, so a chip that does not fit moves
 * whole to the next line instead of breaking mid-word.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun <T> ChipLadder(
    options: List<T>,
    selected: (T) -> Boolean,
    label: (T) -> String,
    tag: (T) -> String,
    onChoose: (T) -> Unit,
) {
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        options.forEach { option ->
            FilterChip(
                selected = selected(option),
                onClick = { onChoose(option) },
                label = { Text(text = label(option), maxLines = 1) },
                modifier = Modifier.testTag(tag(option)),
            )
        }
    }
}

/**
 * Holds the episodic controls (font size, line spacing, typing mode, scrollback,
 * paste, attach) that are not needed while reading output; kept on the screen
 * they cost about 160 dp of permanent height. A bottom sheet, within thumb reach,
 * with room to scroll. Closed, it is not in the composition at all.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TerminalOptionsSheet(
    fontSizeSp: Float,
    onFontSizeChange: (Float) -> Unit,
    gridCols: Int,
    gridRows: Int,
    visibleRows: VisibleRows,
    onVisibleRowsChange: (VisibleRows) -> Unit,
    visibleHeightPx: Int,
    referenceHeightPx: Int,
    scrollbackLines: Int,
    onScrollbackChange: (Int) -> Unit,
    typingMode: TypingMode,
    onTypingModeChange: (TypingMode) -> Unit,
    lineSpacing: TerminalLineSpacing,
    onLineSpacingChange: (TerminalLineSpacing) -> Unit,
    onPaste: () -> Unit,
    onAttach: () -> Unit,
    onShowKeyboard: () -> Unit,
    batteryExempt: Boolean,
    onRequestBatteryExemption: () -> Unit,
    onDismissRequest: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismissRequest,
        sheetState = sheetState,
        modifier = Modifier.testTag(OPTIONS_SHEET_TAG),
    ) {
        TerminalOptionsContent(
            fontSizeSp = fontSizeSp,
            onFontSizeChange = onFontSizeChange,
            gridCols = gridCols,
            gridRows = gridRows,
            visibleRows = visibleRows,
            onVisibleRowsChange = onVisibleRowsChange,
            visibleHeightPx = visibleHeightPx,
            referenceHeightPx = referenceHeightPx,
            scrollbackLines = scrollbackLines,
            onScrollbackChange = onScrollbackChange,
            typingMode = typingMode,
            onTypingModeChange = onTypingModeChange,
            lineSpacing = lineSpacing,
            onLineSpacingChange = onLineSpacingChange,
            onPaste = onPaste,
            onAttach = onAttach,
            onShowKeyboard = onShowKeyboard,
            batteryExempt = batteryExempt,
            onRequestBatteryExemption = onRequestBatteryExemption,
        )
    }
}

/**
 * The sheet content, kept apart from the `ModalBottomSheet` dialog window so its
 * rules can be tested on the JVM without window animations.
 */
@Composable
internal fun TerminalOptionsContent(
    fontSizeSp: Float,
    onFontSizeChange: (Float) -> Unit,
    gridCols: Int,
    gridRows: Int,
    visibleRows: VisibleRows,
    onVisibleRowsChange: (VisibleRows) -> Unit,
    visibleHeightPx: Int,
    referenceHeightPx: Int,
    scrollbackLines: Int,
    onScrollbackChange: (Int) -> Unit,
    typingMode: TypingMode,
    onTypingModeChange: (TypingMode) -> Unit,
    lineSpacing: TerminalLineSpacing,
    onLineSpacingChange: (TerminalLineSpacing) -> Unit,
    onPaste: () -> Unit,
    onAttach: () -> Unit,
    onShowKeyboard: () -> Unit,
    batteryExempt: Boolean,
    onRequestBatteryExemption: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 20.dp)
            .padding(bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(text = "Font size", style = MaterialTheme.typography.titleMedium)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                // With visible lines pinned, the size is derived, so show that
                // instead of the hand-picked value.
                Text(
                    text = if (visibleRows == VisibleRows.AUTOMATIC) {
                        "${fontSizeSp.toInt()}sp"
                    } else {
                        "derived from ${visibleRows.lines} lines"
                    },
                )
                // The resulting grid in numbers: whether `ls -l` fits is a
                // question about columns.
                Text(
                    text = "current grid: $gridCols columns × $gridRows rows" +
                        // Reference and visible heights differ only when something
                        // covers the grid (keyboard, banner, attachment bar). See [GridGeometry].
                        if (referenceHeightPx != visibleHeightPx) {
                            "  ·  ${visibleHeightPx}px of ${referenceHeightPx}px"
                        } else {
                            ""
                        },
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            OutlinedButton(
                onClick = { onFontSizeChange(fontSizeSp - TerminalFontSizePreference.FONT_SIZE_STEP_SP) },
                enabled = fontSizeSp > TerminalFontSizePreference.MIN_FONT_SIZE_SP,
            ) { Text(text = "A-") }
            OutlinedButton(
                onClick = { onFontSizeChange(fontSizeSp + TerminalFontSizePreference.FONT_SIZE_STEP_SP) },
                enabled = fontSizeSp < TerminalFontSizePreference.MAX_FONT_SIZE_SP,
                modifier = Modifier.padding(start = 8.dp),
            ) { Text(text = "A+") }
        }

        HorizontalDivider()

        SectionHeader(
            icon = Icons.Filled.Menu,
            title = "Visible lines",
            caption = if (visibleRows == VisibleRows.AUTOMATIC) {
                "$gridRows fit right now. Pick a number to pin it — the type size adjusts."
            } else {
                "Pinned at ${visibleRows.lines}. The type size is derived from it."
            },
        )
        ChipLadder(
            options = VisibleRows.entries,
            selected = { it == visibleRows },
            label = { it.label },
            tag = ::visibleRowsTag,
            onChoose = onVisibleRowsChange,
        )

        SectionHeader(
            icon = Icons.Filled.List,
            title = "Line spacing",
            caption = "Space between lines, without changing the type size.",
        )
        ChipLadder(
            options = TerminalLineSpacing.entries,
            selected = { it == lineSpacing },
            label = { it.label },
            tag = ::lineSpacingTag,
            onChoose = onLineSpacingChange,
        )

        HorizontalDivider()

        // A switch rather than a fixed choice: shell commands need every key as
        // typed, while prose for the agent benefits from autocorrect. See TypingMode.
        SectionHeader(
            icon = Icons.Filled.Edit,
            title = "Keyboard",
            caption = typingMode.description,
        )
        ChipLadder(
            options = TypingMode.entries,
            selected = { it == typingMode },
            label = { it.label },
            tag = ::typingModeTag,
            onChoose = onTypingModeChange,
        )

        HorizontalDivider()

        // Scrollback sets both the emulator capacity and how much log is fetched
        // when the session opens, hence "scroll back and reread".
        SectionHeader(
            icon = Icons.Filled.Refresh,
            title = "Scrollback",
            caption = "Lines you can scroll back and reread " +
                "(${TerminalScrollback.byRows(scrollbackLines).approxCost} " +
                "on the device). The app fetches this history when it opens the session, " +
                "so it applies from the next time you open it.",
        )
        ChipLadder(
            options = TerminalScrollback.entries,
            selected = { it.lines == scrollbackLines },
            label = { it.label },
            tag = ::scrollbackTag,
            onChoose = { onScrollbackChange(it.lines) },
        )

        HorizontalDivider()

        // Escape hatch to raise the keyboard without tapping the grid, and the
        // only route to it when the program (e.g. `htop`) owns grid taps.
        OutlinedButton(onClick = onShowKeyboard, modifier = Modifier.testTag(SHOW_KEYBOARD_TAG)) {
            Text(text = SHOW_KEYBOARD_LABEL)
        }

        // Copy lives on the floating toolbar, which needs a selection; paste
        // needs an always-available path.
        OutlinedButton(onClick = onPaste, modifier = Modifier.testTag(PASTE_TAG)) {
            Text(text = PASTE_LABEL)
        }

        // Next to Paste because both put a reference to something outside the
        // terminal onto the command line; attach uploads it to the server first
        // so the remote program can open it. Episodic, so it lives in the sheet;
        // upload progress shows on the attachment bar.
        OutlinedButton(onClick = onAttach, modifier = Modifier.testTag(ATTACH_TAG)) {
            Text(text = ATTACH_LABEL)
        }


        // Shown only while the exemption is not granted, and only behind an
        // explicit tap: the app never asks on its own.
        if (!batteryExempt) {
            HorizontalDivider()

            Text(text = "Session in the background", style = MaterialTheme.typography.titleMedium)
            Text(
                text = "Right now Android cuts this app's network a few seconds after you " +
                    "leave the screen, which is why the session reconnects when you come back.",
                style = MaterialTheme.typography.bodySmall,
            )
            OutlinedButton(
                onClick = onRequestBatteryExemption,
                modifier = Modifier.testTag(BATTERY_EXEMPTION_TAG),
            ) {
                Text(text = BATTERY_EXEMPTION_LABEL)
            }
        }

    }
}

/** Test tag for each visible-lines step. */
internal fun visibleRowsTag(option: VisibleRows): String = "linhas-visiveis-${option.lines}"
