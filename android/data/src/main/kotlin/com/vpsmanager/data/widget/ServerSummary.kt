package com.vpsmanager.data.widget

import android.content.Context
import com.vpsmanager.data.dashboard.DashboardSnapshot
import com.vpsmanager.data.dashboard.Severity

/**
 * The summary the home screen widget shows.
 *
 * ## Why there is a separate summary, and not the whole snapshot
 *
 * The widget is drawn by **another process** — the launcher — from data that
 * Android loads off disk. Putting the entire `DashboardSnapshot` through there
 * would mean serialising lists of disks, alerts and scheduled jobs in order to
 * show four numbers. This cut is what fits in a widget and nothing beyond it.
 *
 * ## The field that defines the drawing: [measuredAt]
 *
 * Android **does not accept** a widget update more often than every 30 minutes
 * (`updatePeriodMillis`), and even that interval is a suggestion the system
 * postpones under battery saving. In other words: **a widget is a summary,
 * never a monitor**. The number it shows can be half an hour old.
 *
 * That makes the timestamp mandatory, not decorative. Without it the widget
 * states "disk 78%" wearing the same face as a reading taken just now — which
 * is exactly the lie the offline banner exists to prevent inside the app. A
 * widget with no time is the same failure, on the home screen, where it is
 * seen more times a day.
 */
data class ServerSummary(
    val cpu: String,
    val memory: String,
    val disk: String,
    /** The sentence for the worst state, or `null` when nothing is past its threshold. */
    val alert: String?,
    val worst: Severity,
    /** The DEVICE clock at the instant of the reading. See the class KDoc. */
    val measuredAt: Long,
) {

    companion object {
        /** What to show before any reading — never zeros, which would be a lie. */
        val EMPTY = ServerSummary(
            cpu = "—",
            memory = "—",
            disk = "—",
            alert = null,
            worst = Severity.OK,
            measuredAt = 0L,
        )
    }
}

/**
 * Extracts the summary from a dashboard snapshot.
 *
 * Reuses the ALREADY JUDGED signals (`resourceSignals`, `attention`), never
 * redoes the judgement: two places deciding what counts as "disk full" diverge
 * the day only one of them is fixed — and then the widget would say green with
 * the app saying red, which is worse than having no widget at all.
 */
fun summaryOf(snapshot: DashboardSnapshot, nowMs: Long = System.currentTimeMillis()): ServerSummary {
    val signals = snapshot.resourceSignals.associateBy { it.id }
    val warning = snapshot.attention
    return ServerSummary(
        cpu = signals["cpu"]?.headline ?: "—",
        memory = signals["memoria"]?.headline ?: "—",
        // The disk is `disco:/mnt/x` — the first one to appear is the root,
        // which is the fixed order of `gradeResources`.
        disk = signals.entries.firstOrNull { it.key.startsWith("disco:") }?.value?.headline ?: "—",
        alert = warning.firstOrNull()?.let { "${it.label}: ${it.headline}" },
        worst = warning.firstOrNull()?.severity ?: Severity.OK,
        measuredAt = nowMs,
    )
}

/**
 * Where the summary lives between the app and the widget.
 *
 * `SharedPreferences` and not DataStore: the widget is drawn in a process that
 * Android may wake at any moment, including before the app has ever run.
 * Reading a small value synchronously in that context is exactly the case
 * where `SharedPreferences` is still right — DataStore would force a coroutine
 * inside the widget provider just to read four strings.
 */
object StoredSummary {

    private const val FILE = "vpsm_resumo_widget"

    fun persist(context: Context, summary: ServerSummary) {
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            .putString("cpu", summary.cpu)
            .putString("memoria", summary.memory)
            .putString("disco", summary.disk)
            .putString("alerta", summary.alert)
            .putString("pior", summary.worst.storedName)
            .putLong("medidoEm", summary.measuredAt)
            .apply()
    }

    fun read(context: Context): ServerSummary {
        val p = context.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val measuredAt = p.getLong("medidoEm", 0L)
        // With no reading ever written, hand back EMPTY instead of null
        // strings turned into "null" on screen.
        if (measuredAt == 0L) return ServerSummary.EMPTY
        return ServerSummary(
            cpu = p.getString("cpu", "—").orEmpty(),
            memory = p.getString("memoria", "—").orEmpty(),
            disk = p.getString("disco", "—").orEmpty(),
            alert = p.getString("alerta", null),
            worst = runCatching { Severity.fromStoredName(p.getString("pior", "OK").orEmpty()) }
                .getOrDefault(Severity.OK),
            measuredAt = measuredAt,
        )
    }
}

/**
 * How long ago the summary was measured, in words.
 *
 * "now" under a minute, then minutes, then hours. Past a day the sentence
 * becomes "more than a day ago" instead of counting: the exact number of days
 * changes no decision, and what matters is that the data is no longer good.
 */
fun ageInWords(measuredAt: Long, nowMs: Long = System.currentTimeMillis()): String {
    if (measuredAt <= 0L) return "no reading yet"
    val minutes = ((nowMs - measuredAt) / 60_000L).coerceAtLeast(0L)
    return when {
        minutes < 1 -> "just now"
        minutes < 60 -> "$minutes min ago"
        minutes < 24 * 60 -> "${minutes / 60} h ago"
        else -> "over a day ago"
    }
}
