package dev.servercontrolpanel.data.widget

import android.content.Context
import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.Severity

/**
 * The summary the home screen widget shows: only what fits in a widget, not the
 * whole `DashboardSnapshot`, since the launcher process reads it from disk.
 *
 * Android updates widgets at most every 30 minutes (and postpones that under
 * battery saving), so a widget is a summary, never a monitor. [measuredAt] is
 * therefore mandatory: without it a stale value looks like a fresh reading.
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
 * Extracts the summary from a dashboard snapshot. It reuses the already judged
 * signals (`resourceSignals`, `attention`) so the widget and the app can never
 * disagree about what counts as a problem.
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
 * Where the summary lives between the app and the widget. `SharedPreferences`
 * rather than DataStore because the widget process may start before the app ever
 * ran and needs a small synchronous read.
 */
object StoredSummary {

    private const val FILE = "panel_resumo_widget"

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
 * How long ago the summary was measured, in words. Past a day it just says "over
 * a day ago": the exact count changes no decision.
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
