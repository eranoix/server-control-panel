package com.vpsmanager.app.widget

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
import com.vpsmanager.app.MainActivity
import com.vpsmanager.data.dashboard.Severity
import com.vpsmanager.data.widget.StoredSummary
import com.vpsmanager.data.widget.ageInWords

/**
 * The home-screen widget: the server without opening the app.
 *
 * ## Why this is the highest-return feature for an operator
 *
 * Everything else in this app competes for the attention of someone who
 * has already decided to open it. The widget serves the question asked
 * **before** that decision — *do I need to open it?* — and the right
 * answer, on the vast majority of days, is no. A panel that saves you
 * opening it is the only one that works while nobody is looking.
 *
 * ## The platform limit that DEFINES the design
 *
 * Android accepts no refresh more frequent than 30 minutes
 * (`updatePeriodMillis`), and even that deadline is deferred under battery
 * saving. In other words: **this is a summary, never a monitor**. The
 * number here may be half an hour old.
 *
 * That is why the data's timestamp has a fixed place in the layout, and is
 * not a detail to be cut when space gets tight. A widget that claims "disk
 * 78%" with the face of a reading taken just now commits, on the home
 * screen, the same lie the offline banner exists to prevent inside the app
 * — and commits it more times a day, because the home screen is seen more
 * often.
 *
 * ## Why it does not fetch data on its own
 *
 * What writes the summary is the app, on every successful read of the
 * panel. The widget only READS. Making the widget fetch would mean
 * networking in a process the system wakes with no warning, with a session
 * that may have expired and with nobody there to see an error — three
 * things that, together, produce a widget stuck "loading" forever without
 * explaining why.
 *
 * The consequence is honest and it is on the screen: whoever has not
 * opened the app for a day sees "more than a day ago", not an invented
 * number.
 */
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
            // The tap opens the app. A widget with no destination is a poster:
            // the question it answers ("do I need to open it?") has "yes" as one
            // of the answers, and in that case the next gesture has to be a
            // single one.
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
            // THE DATA'S TIMESTAMP, at the top and always. See the class KDoc.
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

        // The alert row only exists when there is an alert. A permanent "all
        // fine" would take up, every day, the row that on the bad day carries
        // the only information that matters.
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

/**
 * One of the widget's measurements.
 *
 * It takes the [modifier] instead of building its own: `defaultWeight()`
 * only exists INSIDE the scope of a Glance `Row`/`Column`, and this
 * composable is free-standing. Whoever has the scope is who distributes
 * the weight.
 */
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

/** Glance's `sp` comes from `androidx.compose.ui.unit`; this shortcut avoids the import at each call site. */
private fun Int.sp() = androidx.compose.ui.unit.TextUnit(
    this.toFloat(),
    androidx.compose.ui.unit.TextUnitType.Sp,
)

/**
 * The receiver Android instantiates. Without it the widget does not exist
 * for the system, however correct the design may be.
 */
class HomeWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = HomeWidget()
}
