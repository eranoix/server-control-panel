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

/** Test tags — the interface is checked through them, never through loose text. */
internal const val TAG_POSITION_BAR = "terminal-barra-posicao"
internal const val TAG_BACK_TO_END = "terminal-voltar-ao-fim"

/**
 * What the owner needs to see while reading the past: **where they are** and
 * **how to get back**.
 *
 * Scrolling blind in a terminal is disorienting — there is no section title,
 * no page number, and the content all looks alike. The bar on the right
 * answers "where am I"; the button answers "how do I get out of here"; and
 * when new output arrives while you are reading, the button says so, because
 * the screen does **not** jump to it on its own.
 *
 * Everything appears only when it makes sense: pinned to the bottom, the
 * terminal stays clean.
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
 * The bar on the right. It is not a control — it is an indicator: dragging is
 * the gesture on the whole grid, and a thin 4 dp handle would be a worse
 * target than the entire screen that already works.
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

/**
 * The way back. It sits at the bottom, where the thumb reaches, and says **how
 * far** up you went — a number orients better than an arrow alone.
 */
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
