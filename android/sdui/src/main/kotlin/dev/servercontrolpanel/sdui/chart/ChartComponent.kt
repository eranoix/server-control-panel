package dev.servercontrolpanel.sdui.chart

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.sdui.data.ComponentDataState
import dev.servercontrolpanel.sdui.data.rememberComponentDataState
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonPrimitive

private const val CHART_HEIGHT_DP = 200

/**
 * Renders a [SduiComponent.Chart] with a hand-drawn Compose [Canvas] — line
 * or bar only (T-07-SC accepted this instead of pulling in a charting
 * library). Each row's [SduiComponent.Chart.yKey] value becomes one plotted
 * point/bar, in the order the server returned the rows; [SduiComponent.Chart.xKey]
 * only labels the axis, it never reorders or aggregates the series.
 */
@Composable
fun ChartComponent(component: SduiComponent.Chart) {
    when (val state = rememberComponentDataState(component.seriesSource).value) {
        is ComponentDataState.Loading -> LoadingBlock()
        is ComponentDataState.Error -> ErrorBlock(state.reason)
        is ComponentDataState.Empty -> EmptyBlock()
        is ComponentDataState.Data -> {
            val values = state.rows.mapNotNull { it.numericValue(component.yKey) }
            if (values.isEmpty()) {
                ErrorBlock("The series has no numeric values in \"${component.yKey}\".")
            } else {
                when (component.chartKind) {
                    "bar" -> BarChart(values)
                    else -> LineChart(values) // "line" and any other value default to line.
                }
            }
        }
    }
}

private fun JsonObject.numericValue(key: String): Double? = this[key]?.jsonPrimitive?.doubleOrNull

@Composable
private fun LineChart(values: List<Double>) {
    val color = MaterialTheme.colorScheme.primary
    val min = values.min()
    val max = values.max()
    val range = (max - min).takeIf { it != 0.0 } ?: 1.0

    Canvas(
        modifier = Modifier
            .fillMaxWidth()
            .height(CHART_HEIGHT_DP.dp)
            .padding(8.dp),
    ) {
        val stepX = if (values.size > 1) size.width / (values.size - 1) else 0f
        val points = values.mapIndexed { index, value ->
            val x = stepX * index
            val y = size.height - ((value - min) / range).toFloat() * size.height
            Offset(x, y)
        }
        for (i in 0 until points.size - 1) {
            drawLine(color = color, start = points[i], end = points[i + 1], strokeWidth = 4f)
        }
    }
}

@Composable
private fun BarChart(values: List<Double>) {
    val color = MaterialTheme.colorScheme.primary
    val min = minOf(0.0, values.min())
    val max = values.max()
    val range = (max - min).takeIf { it != 0.0 } ?: 1.0

    Canvas(
        modifier = Modifier
            .fillMaxWidth()
            .height(CHART_HEIGHT_DP.dp)
            .padding(8.dp),
    ) {
        val barWidth = size.width / values.size
        values.forEachIndexed { index, value ->
            val barHeight = ((value - min) / range).toFloat() * size.height
            drawRect(
                color = color,
                topLeft = Offset(index * barWidth, size.height - barHeight),
                size = androidx.compose.ui.geometry.Size(barWidth * 0.8f, barHeight),
            )
        }
    }
}

@Composable
private fun LoadingBlock() {
    Box(modifier = Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorBlock(reason: String) {
    Text(text = reason, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
}

@Composable
private fun EmptyBlock() {
    Text(text = "Nothing to show.", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(16.dp))
}
