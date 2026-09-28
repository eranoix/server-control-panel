package dev.servercontrolpanel.data.widget

import android.content.Context
import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.Severity

data class ServerSummary(
    val cpu: String,
    val memory: String,
    val disk: String,
    val alert: String?,
    val worst: Severity,
    val measuredAt: Long,
) {

    companion object {
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

fun summaryOf(snapshot: DashboardSnapshot, nowMs: Long = System.currentTimeMillis()): ServerSummary {
    val signals = snapshot.resourceSignals.associateBy { it.id }
    val warning = snapshot.attention
    return ServerSummary(
        cpu = signals["cpu"]?.headline ?: "—",
        memory = signals["memory"]?.headline ?: "—",
        disk = signals.entries.firstOrNull { it.key.startsWith("disco:") }?.value?.headline ?: "—",
        alert = warning.firstOrNull()?.let { "${it.label}: ${it.headline}" },
        worst = warning.firstOrNull()?.severity ?: Severity.OK,
        measuredAt = nowMs,
    )
}

object StoredSummary {

    private const val FILE = "panel_widget_summary"

    fun persist(context: Context, summary: ServerSummary) {
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            .putString("cpu", summary.cpu)
            .putString("memory", summary.memory)
            .putString("disco", summary.disk)
            .putString("alert", summary.alert)
            .putString("worst", summary.worst.storedName)
            .putLong("measuredAt", summary.measuredAt)
            .apply()
    }

    fun read(context: Context): ServerSummary {
        val p = context.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        val measuredAt = p.getLong("measuredAt", 0L)
        if (measuredAt == 0L) return ServerSummary.EMPTY
        return ServerSummary(
            cpu = p.getString("cpu", "—").orEmpty(),
            memory = p.getString("memory", "—").orEmpty(),
            disk = p.getString("disco", "—").orEmpty(),
            alert = p.getString("alert", null),
            worst = runCatching { Severity.fromStoredName(p.getString("worst", "OK").orEmpty()) }
                .getOrDefault(Severity.OK),
            measuredAt = measuredAt,
        )
    }
}

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
