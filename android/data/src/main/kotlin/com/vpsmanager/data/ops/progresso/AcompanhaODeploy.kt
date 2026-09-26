package com.vpsmanager.data.ops.progresso

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.Data
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.vpsmanager.data.ops.DeployStatusResult
import com.vpsmanager.data.ops.OpsRepository
import kotlinx.coroutines.delay

/**
 * Follows a deploy through to the end and shows its progress in a notification.
 *
 * ## The debt this pays
 *
 * Triggering a deploy from the phone left the person **with no signal at all
 * for up to ten minutes**: the screen showed "sent" and then nothing, because
 * leaving the app kills the socket and Android 15 cuts the process's network in
 * the background after ~5.7 s. Whoever triggers a deploy from a phone almost
 * always triggers it and puts the phone away — which is exactly the gesture the
 * app did not cover.
 *
 * ## Why a notification and not a screen
 *
 * The value of a long deploy is in **not having to watch**. A screen that has
 * to stay open to follow along follows nothing — it holds the person captive. A
 * progress notification is the opposite: it seeks the person out when the state
 * changes, and disappears by itself when it is over.
 *
 * ## Why this is the foreground-service use that survived
 *
 * Android 13/14/15 closed almost every door to background work; `dataSync` with
 * a foreground service is capped at 6 h a day on Android 15 and is being
 * deprecated. A deploy lasts minutes and has a **beginning, visible progress
 * and an end** — it is the case the platform still accepts readily, and
 * `WorkManager` handles the promotion to the foreground by itself.
 *
 * ## The end is always stated
 *
 * Success, failure and "I gave up asking" produce different notifications. A
 * progress bar that simply disappears is indistinguishable from an app that
 * died — and after a deploy, "I don't know what happened" is the worst possible
 * state.
 */
class AcompanhaODeployWorker(
    context: Context,
    params: WorkerParameters,
    private val repositorio: OpsRepository = OpsRepository(),
) : CoroutineWorker(context, params) {

    override suspend fun getForegroundInfo(): ForegroundInfo =
        infoDeProgresso(applicationContext, id.hashCode(), "Deploy in progress", null, null)

    override suspend fun doWork(): Result {
        val jobId = inputData.getString(CHAVE_JOB) ?: return Result.failure()
        val app = inputData.getString(CHAVE_APP).orEmpty()

        setForeground(getForegroundInfo())

        var tentativasSemResposta = 0
        repeat(MAXIMO_DE_LEITURAS) {
            when (val r = repositorio.fetchDeployStatus(jobId)) {
                is DeployStatusResult.Success -> {
                    tentativasSemResposta = 0
                    val s = r.status
                    if (terminou(s.status)) {
                        avisarOFim(applicationContext, app, s.status, s.error)
                        return Result.success()
                    }
                    setForeground(
                        infoDeProgresso(
                            applicationContext,
                            id.hashCode(),
                            tituloDe(app),
                            s.step,
                            s.progress.toInt(),
                        ),
                    )
                }
                is DeployStatusResult.Error -> {
                    // A READ failure is not a deploy failure: the device's
                    // network may have blinked while the server carries on
                    // working. Giving up on the first one would report the
                    // wrong news about the most expensive thing on the screen.
                    tentativasSemResposta++
                    if (tentativasSemResposta >= LEITURAS_FALHAS_ATE_DESISTIR) {
                        avisarPerdaDeContato(applicationContext, app)
                        return Result.success()
                    }
                }
            }
            delay(INTERVALO_MS)
        }
        avisarPerdaDeContato(applicationContext, app)
        return Result.success()
    }

    private fun tituloDe(app: String) = if (app.isBlank()) "Deploy" else "Deploy · $app"

    companion object {
        const val CHAVE_JOB = "job_id"
        const val CHAVE_APP = "app"

        /**
         * 3 s. It is the same interval as the server's `/ops/status` cache:
         * asking faster brings back no new number, it only burns radio.
         */
        private const val INTERVALO_MS = 3_000L

        /** ~15 min of following along. A deploy longer than that is news in itself. */
        private const val MAXIMO_DE_LEITURAS = 300

        /** Three failures in a row (~9 s) is no longer a network blink. */
        private const val LEITURAS_FALHAS_ATE_DESISTIR = 3

        private const val NOME_DO_TRABALHO = "acompanha-deploy"

        /**
         * Starts following [jobId].
         *
         * `REPLACE` and not `APPEND`: two deploys followed at once would
         * produce two progress bars competing for the same row of the shade,
         * and the second is always the one that matters.
         */
        fun acompanhar(context: Context, jobId: String, app: String) {
            val pedido = OneTimeWorkRequestBuilder<AcompanhaODeployWorker>()
                .setInputData(dados(jobId, app))
                .build()
            WorkManager.getInstance(context)
                .beginUniqueWork(NOME_DO_TRABALHO, ExistingWorkPolicy.REPLACE, pedido)
                .enqueue()
        }

        fun dados(jobId: String, app: String): Data =
            workDataOf(CHAVE_JOB to jobId, CHAVE_APP to app)

        /**
         * Whether this state is final.
         *
         * An ALLOW LIST of terminal states, with everything else being "still
         * running". The opposite — listing the in-flight states — would make a
         * new server state (`verifying`, say) read as an ending, and the
         * notification would announce a finished deploy that is still mid-way.
         */
        fun terminou(estado: String): Boolean =
            estado.lowercase() in setOf("ok", "success", "succeeded", "done", "failed", "error", "rolled_back", "cancelled", "canceled")

        fun deuCerto(estado: String): Boolean =
            estado.lowercase() in setOf("ok", "success", "succeeded", "done")
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// The notifications
// ─────────────────────────────────────────────────────────────────────────────

private const val CANAL = "vpsm_progresso"
private const val ID_DO_FIM = 0x0DEB

private fun garantirCanal(context: Context) {
    val gerente = context.getSystemService(NotificationManager::class.java) ?: return
    gerente.createNotificationChannel(
        // IMPORTANCE_LOW: progress neither rings nor vibrates. A ten-minute
        // deploy that beeped at every step would be uninstalled on day two.
        NotificationChannel(CANAL, "Task progress", NotificationManager.IMPORTANCE_LOW),
    )
}

private fun infoDeProgresso(
    context: Context,
    id: Int,
    titulo: String,
    passo: String?,
    percentual: Int?,
): ForegroundInfo {
    garantirCanal(context)
    val b = NotificationCompat.Builder(context, CANAL)
        .setContentTitle(titulo)
        .setSmallIcon(android.R.drawable.stat_sys_upload)
        .setOngoing(true)
        .setOnlyAlertOnce(true)
    if (percentual != null && percentual in 0..100) {
        b.setProgress(100, percentual, false)
        // The STEP is worth more than the number. "78%" does not tell you
        // whether you can breathe; "restarting the service" does. The
        // percentage goes alongside it, never alone.
        b.setContentText(passo?.takeIf { it.isNotBlank() }?.let { "$it · $percentual%" } ?: "$percentual%")
    } else {
        b.setProgress(0, 0, true)
        b.setContentText(passo?.takeIf { it.isNotBlank() } ?: "In progress…")
    }
    return ForegroundInfo(id, b.build(), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
}

private fun avisarOFim(context: Context, app: String, estado: String, erro: String?) {
    garantirCanal(context)
    val ok = AcompanhaODeployWorker.deuCerto(estado)
    val gerente = context.getSystemService(NotificationManager::class.java) ?: return
    gerente.notify(
        ID_DO_FIM,
        NotificationCompat.Builder(context, CANAL)
            .setContentTitle(if (ok) "Deploy finished" else "Deploy failed")
            .setContentText(
                buildString {
                    if (app.isNotBlank()) append("$app · ")
                    append(estado)
                    erro?.takeIf { it.isNotBlank() }?.let { append(" · $it") }
                },
            )
            .setSmallIcon(android.R.drawable.stat_sys_upload_done)
            .setAutoCancel(true)
            // THE END ALERTS, the progress does not. It is the only news in
            // this series that changes what the person does next.
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build(),
    )
}

/**
 * The most important case of all: the app lost contact and **does not know**
 * how the deploy ended.
 *
 * Saying so is mandatory. A bar that vanishes in silence is read as "it
 * finished fine" — and announcing a success you did not verify is the worst
 * thing a deploy notification can do.
 */
private fun avisarPerdaDeContato(context: Context, app: String) {
    garantirCanal(context)
    val gerente = context.getSystemService(NotificationManager::class.java) ?: return
    gerente.notify(
        ID_DO_FIM,
        NotificationCompat.Builder(context, CANAL)
            .setContentTitle("No news from the deploy")
            .setContentText(
                (if (app.isNotBlank()) "$app · " else "") +
                    "the app can no longer check on it. The deploy may have finished — open to check.",
            )
            .setStyle(NotificationCompat.BigTextStyle())
            .setSmallIcon(android.R.drawable.stat_sys_warning)
            .setAutoCancel(true)
            .build(),
    )
}
