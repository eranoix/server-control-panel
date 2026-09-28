package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp

const val COMPOSITION_STRIP_TAG = "composition-strip"

private val BAND_HEIGHT = 28.dp

@Composable
fun CompositionStrip(
    text: String,
    modifier: Modifier = Modifier,
    reserveSpace: Boolean = true,
) {
    if (text.isEmpty() && !reserveSpace) return

    Row(
        modifier = modifier
            .fillMaxWidth()
            .height(BAND_HEIGHT)
            .testTag(COMPOSITION_STRIP_TAG)
            .background(
                if (text.isEmpty()) {
                    Color.Transparent
                } else {
                    MaterialTheme.colorScheme.surfaceVariant
                },
            )
            .padding(horizontal = 12.dp, vertical = 4.dp)
            .horizontalScroll(rememberScrollState()),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            fontFamily = FontFamily.Monospace,
            textDecoration = TextDecoration.Underline,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
    }
}
