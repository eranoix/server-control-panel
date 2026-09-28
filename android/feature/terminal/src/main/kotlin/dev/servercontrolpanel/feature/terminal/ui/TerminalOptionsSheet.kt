package dev.servercontrolpanel.feature.terminal.ui

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
import dev.servercontrolpanel.feature.terminal.attach.ATTACH_LABEL
import dev.servercontrolpanel.feature.terminal.attach.ATTACH_TAG
import dev.servercontrolpanel.feature.terminal.prefs.TerminalLineSpacing
import dev.servercontrolpanel.feature.terminal.power.BATTERY_EXEMPTION_LABEL
import dev.servercontrolpanel.feature.terminal.power.BATTERY_EXEMPTION_TAG
import dev.servercontrolpanel.feature.terminal.prefs.VisibleRows
import dev.servercontrolpanel.feature.terminal.prefs.TypingMode
import dev.servercontrolpanel.feature.terminal.prefs.TerminalScrollback
import dev.servercontrolpanel.feature.terminal.prefs.TerminalFontSizePreference

const val OPTIONS_SHEET_TAG = "terminal-options-sheet"

const val OPTIONS_DESCRIPTION = "Terminal options"

const val SHOW_KEYBOARD_LABEL = "Show keyboard"

const val SHOW_KEYBOARD_TAG = "show-keyboard-button"

const val PASTE_LABEL = "Paste"

const val PASTE_TAG = "paste-button"

fun lineSpacingTag(step: TerminalLineSpacing): String = "line-spacing-${step.name}"

fun typingModeTag(mode: TypingMode): String = "mode-typing-${mode.name}"

fun scrollbackTag(step: TerminalScrollback): String = "scrollback-${step.name}"


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
                Text(
                    text = if (visibleRows == VisibleRows.AUTOMATIC) {
                        "${fontSizeSp.toInt()}sp"
                    } else {
                        "derived from ${visibleRows.lines} lines"
                    },
                )
                Text(
                    text = "current grid: $gridCols columns × $gridRows rows" +
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

        OutlinedButton(onClick = onShowKeyboard, modifier = Modifier.testTag(SHOW_KEYBOARD_TAG)) {
            Text(text = SHOW_KEYBOARD_LABEL)
        }

        OutlinedButton(onClick = onPaste, modifier = Modifier.testTag(PASTE_TAG)) {
            Text(text = PASTE_LABEL)
        }

        OutlinedButton(onClick = onAttach, modifier = Modifier.testTag(ATTACH_TAG)) {
            Text(text = ATTACH_LABEL)
        }


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

internal fun visibleRowsTag(option: VisibleRows): String = "lines-visible-${option.lines}"
