package com.vpsmanager.feature.terminal.scroll

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.terminalengine.TerminalScrollState

/** Test tags: the UI is checked through them, never through loose text. */
internal const val TAG_POSITION_BAR = "terminal-barra-posicao"
internal const val TAG_BACK_TO_END = "terminal-voltar-ao-fim"

/**
 * Shows where the user is while reading history and how to get back: a position
 * bar on the right and a "back to the end" button that also announces new output
 * (the screen never jumps to it on its own). Hidden when pinned to the bottom.
 */
@Composable
internal fun ScrollPositionOverlay(
    state: TerminalScrollState,
    hasNewOutput: Boolean,
    onBackToEnd: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val readingThePast = !state.atEnd && state.canScroll

    Box(modifier = modifier.fillMaxSize()) {
        AnimatedVisibility(
            visible = readingThePast,
            enter = fadeIn(),
            exit = fadeOut(),
            modifier = Modifier.align(Alignment.CenterEnd),
        ) {
            PositionBar(state = state)
        }

        AnimatedVisibility(
            visible = readingThePast,
            enter = fadeIn(),
            exit = fadeOut(),
            modifier = Modifier.align(Alignment.BottomCenter),
        ) {
            BackToEndButton(
                rowsBack = state.history - state.offset,
                hasNewOutput = hasNewOutput,
                onBackToEnd = onBackToEnd,
            )
        }
    }
}

/**
 * The position bar is an indicator, not a control: dragging works on the whole
 * grid, which is a better target than a 4 dp handle.
 */
@Composable
private fun PositionBar(state: TerminalScrollState) {
    val relativeHeight = if (state.total > 0) {
        (state.visible.toFloat() / state.total.toFloat()).coerceIn(0.08f, 1f)
    } else {
        1f
    }

    BoxWithConstraints(
        modifier = Modifier
            .padding(end = 3.dp)
            .fillMaxHeight(0.6f)
            .width(4.dp)
            .clip(RoundedCornerShape(2.dp))
            .background(MaterialTheme.colorScheme.onSurface.copy(alpha = 0.12f))
            .testTag(TAG_POSITION_BAR)
            .semantics {
                contentDescription =
                    "Position in history: ${(state.progress * 100).toInt()} percent"
            },
    ) {
        val thumbHeight = maxHeight * relativeHeight
        val cursorOffset = (maxHeight - thumbHeight) * state.progress
        Box(
            modifier = Modifier
                .offset(y = cursorOffset)
                .width(4.dp)
                .size(width = 4.dp, height = thumbHeight)
                .clip(RoundedCornerShape(2.dp))
                .background(MaterialTheme.colorScheme.primary),
        )
    }
}

/** The way back, within thumb reach, showing how many lines up the user is. */
@Composable
private fun BackToEndButton(
    rowsBack: Long,
    hasNewOutput: Boolean,
    onBackToEnd: () -> Unit,
) {
    val label = when {
        hasNewOutput -> "New output at the end"
        rowsBack > 0 -> "Back to the end · $rowsBack lines"
        else -> "Back to the end"
    }

    Surface(
        onClick = onBackToEnd,
        shape = CircleShape,
        color = if (hasNewOutput) {
            MaterialTheme.colorScheme.primary
        } else {
            MaterialTheme.colorScheme.secondaryContainer
        },
        contentColor = if (hasNewOutput) {
            MaterialTheme.colorScheme.onPrimary
        } else {
            MaterialTheme.colorScheme.onSecondaryContainer
        },
        tonalElevation = 3.dp,
        shadowElevation = 3.dp,
        modifier = Modifier
            .padding(bottom = 12.dp)
            .testTag(TAG_BACK_TO_END)
            .semantics { contentDescription = label },
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.Center,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
        ) {
            if (hasNewOutput) {
                Box(
                    modifier = Modifier
                        .padding(end = 8.dp)
                        .size(8.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.onPrimary),
                )
            }
            Text(text = label, style = MaterialTheme.typography.labelLarge)
        }
    }
}
