package dev.servercontrolpanel.app.widget

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.dp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.GlanceTheme
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.provideContent
import androidx.glance.background
import androidx.glance.layout.Alignment
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.padding
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextStyle
import androidx.glance.action.actionStartActivity
import dev.servercontrolpanel.app.MainActivity
import dev.servercontrolpanel.data.dashboard.Severity
import dev.servercontrolpanel.data.widget.StoredSummary
import dev.servercontrolpanel.data.widget.ageInWords

class HomeWidget : GlanceAppWidget() {

    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val summary = StoredSummary.read(context)
        provideContent {
            GlanceTheme {
                Content(
                    cpu = summary.cpu,
                    memory = summary.memory,
                    disk = summary.disk,
                    alert = summary.alert,
                    worst = summary.worst,
                    age = ageInWords(summary.measuredAt),
                )
            }
        }
    }
}

@Composable
private fun Content(
    cpu: String,
    memory: String,
    disk: String,
    alert: String?,
    worst: Severity,
    age: String,
) {
    Column(
        modifier = GlanceModifier
            .fillMaxSize()
            .background(GlanceTheme.colors.widgetBackground)
            .padding(12.dp)
            .clickable(actionStartActivity<MainActivity>()),
    ) {
        Row(modifier = GlanceModifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "SERVER",
                style = TextStyle(
                    fontSize = 10.sp(),
                    fontWeight = FontWeight.Bold,
                    color = GlanceTheme.colors.onSurfaceVariant,
                ),
                modifier = GlanceModifier.defaultWeight(),
            )
            Text(
                text = age,
                style = TextStyle(fontSize = 10.sp(), color = GlanceTheme.colors.onSurfaceVariant),
            )
        }

        Row(modifier = GlanceModifier.fillMaxWidth().padding(top = 6.dp)) {
            Metric(label = "CPU", value = cpu, modifier = GlanceModifier.defaultWeight())
            Metric(label = "RAM", value = memory, modifier = GlanceModifier.defaultWeight())
            Metric(label = "DISK", value = disk, modifier = GlanceModifier.defaultWeight())
        }

        if (alert != null) {
            Text(
                text = if (worst == Severity.CRITICAL) "CRITICAL · $alert" else "WARNING · $alert",
                style = TextStyle(
                    fontSize = 11.sp(),
                    fontWeight = FontWeight.Bold,
                    color = if (worst == Severity.CRITICAL) {
                        GlanceTheme.colors.error
                    } else {
                        GlanceTheme.colors.onSurface
                    },
                ),
                modifier = GlanceModifier.padding(top = 6.dp),
            )
        }
    }
}

@Composable
private fun Metric(label: String, value: String, modifier: GlanceModifier = GlanceModifier) {
    Column(modifier = modifier) {
        Text(
            text = label,
            style = TextStyle(fontSize = 9.sp(), color = GlanceTheme.colors.onSurfaceVariant),
        )
        Text(
            text = value,
            style = TextStyle(
                fontSize = 18.sp(),
                fontWeight = FontWeight.Bold,
                color = GlanceTheme.colors.onSurface,
            ),
        )
    }
}

private fun Int.sp() = androidx.compose.ui.unit.TextUnit(
    this.toFloat(),
    androidx.compose.ui.unit.TextUnitType.Sp,
)

class HomeWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = HomeWidget()
}
