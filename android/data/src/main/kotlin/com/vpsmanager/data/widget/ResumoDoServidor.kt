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
 * ## The field that defines the drawing: [medidoEm]
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
data class ResumoDoServidor(
    val cpu: String,
    val memoria: String,
    val disco: String,
    /** The sentence for the worst state, or `null` when nothing is past its threshold. */
    val alerta: String?,
    val pior: Severity,
    /** The DEVICE clock at the instant of the reading. See the class KDoc. */
    val medidoEm: Long,
) {

    companion object {
        /** What to show before any reading — never zeros, which would be a lie. */
        val VAZIO = ResumoDoServidor(
            cpu = "—",
            memoria = "—",
            disco = "—",
            alerta = null,
            pior = Severity.OK,
            medidoEm = 0L,
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
fun resumoDe(snapshot: DashboardSnapshot, agoraMs: Long = System.currentTimeMillis()): ResumoDoServidor {
    val sinais = snapshot.resourceSignals.associateBy { it.id }
    val atencao = snapshot.attention
    return ResumoDoServidor(
        cpu = sinais["cpu"]?.headline ?: "—",
        memoria = sinais["memoria"]?.headline ?: "—",
        // The disk is `disco:/mnt/x` — the first one to appear is the root,
        // which is the fixed order of `gradeResources`.
        disco = sinais.entries.firstOrNull { it.key.startsWith("disco:") }?.value?.headline ?: "—",
        alerta = atencao.firstOrNull()?.let { "${it.label}: ${it.headline}" },
        pior = atencao.firstOrNull()?.severity ?: Severity.OK,
        medidoEm = agoraMs,
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
object ResumoGuardado {

    private const val ARQUIVO = "vpsm_resumo_widget"

    fun gravar(context: Context, resumo: ResumoDoServidor) {
        context.getSharedPreferences(ARQUIVO, Context.MODE_PRIVATE).edit()
            .putString("cpu", resumo.cpu)
            .putString("memoria", resumo.memoria)
            .putString("disco", resumo.disco)
            .putString("alerta", resumo.alerta)
            .putString("pior", resumo.pior.name)
            .putLong("medidoEm", resumo.medidoEm)
            .apply()
    }

    fun ler(context: Context): ResumoDoServidor {
        val p = context.getSharedPreferences(ARQUIVO, Context.MODE_PRIVATE)
        val medidoEm = p.getLong("medidoEm", 0L)
        // With no reading ever written, hand back EMPTY instead of null
        // strings turned into "null" on screen.
        if (medidoEm == 0L) return ResumoDoServidor.VAZIO
        return ResumoDoServidor(
            cpu = p.getString("cpu", "—").orEmpty(),
            memoria = p.getString("memoria", "—").orEmpty(),
            disco = p.getString("disco", "—").orEmpty(),
            alerta = p.getString("alerta", null),
            pior = runCatching { Severity.valueOf(p.getString("pior", "OK").orEmpty()) }
                .getOrDefault(Severity.OK),
            medidoEm = medidoEm,
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
fun idadeEmPalavras(medidoEm: Long, agoraMs: Long = System.currentTimeMillis()): String {
    if (medidoEm <= 0L) return "no reading yet"
    val minutos = ((agoraMs - medidoEm) / 60_000L).coerceAtLeast(0L)
    return when {
        minutos < 1 -> "just now"
        minutos < 60 -> "$minutos min ago"
        minutos < 24 * 60 -> "${minutos / 60} h ago"
        else -> "over a day ago"
    }
}
