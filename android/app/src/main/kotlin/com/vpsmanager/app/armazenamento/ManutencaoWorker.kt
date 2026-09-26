package com.vpsmanager.app.armazenamento

import android.content.Context
import android.util.Log
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.vpsmanager.data.armazenamento.ArmazenamentoDoApp
import com.vpsmanager.data.armazenamento.DepositosDoAparelho
import java.util.concurrent.TimeUnit

/**
 * The periodic storage cleanup.
 *
 * ## Why periodic, and not "on open"
 *
 * Doing it at launch delays exactly the moment the person is waiting for
 * the screen, and it is the moment the temporary files are MOST likely in
 * use (an upload resuming). `WorkManager` runs when the device is
 * comfortable, survives a reboot and wakes nothing up on its own.
 *
 * ## Why once a day
 *
 * What this routine removes is temporary files three days old and packages
 * of versions already installed. Neither accumulates in minutes. Running
 * more often would spend battery re-reading the same directories and would
 * not give back a single extra byte.
 *
 * ## Why only `BatteryNotLow`
 *
 * Sweeping a few directories is cheap, so requiring a charger or idle time
 * would delay the cleanup by days in the name of a saving that does not
 * exist. The only restriction that justifies itself is not doing this on a
 * dying battery — there, any non-essential work should get out of the way.
 *
 * Failure is never `retry`: if a file could not be deleted today (it was
 * open), tomorrow's pass picks it up. Rescheduling for that would only
 * pile work up.
 */
class ManutencaoWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val resultado = runCatching {
            // `emUso` empty: when this routine runs, no update is being
            // downloaded — the download happens in the foreground, from the tap
            // on the banner, and does not compete with background work. If one
            // day it starts happening here, THIS is the point that needs to know.
            DepositosDoAparelho.manutencao(applicationContext)
        }.getOrElse {
            Log.w(TAG, "manutenção de armazenamento falhou", it)
            return Result.success()
        }

        if (resultado.arquivosRemovidos > 0) {
            Log.i(
                TAG,
                "manutenção: ${resultado.arquivosRemovidos} arquivo(s), " +
                    "${ArmazenamentoDoApp.formatar(resultado.liberadoBytes)} liberados",
            )
        }
        return Result.success()
    }

    companion object {
        private const val TAG = "VPSMManutencao"
        private const val NOME_DO_TRABALHO = "manutencao-de-armazenamento"

        fun enfileirar(context: Context) {
            val pedido = PeriodicWorkRequestBuilder<ManutencaoWorker>(1, TimeUnit.DAYS)
                .setConstraints(Constraints.Builder().setRequiresBatteryNotLow(true).build())
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                NOME_DO_TRABALHO,
                // KEEP, never REPLACE: a REPLACE on every app open restarts the
                // period's window, and an app opened every day would never get to
                // run the cleanup even once.
                ExistingPeriodicWorkPolicy.KEEP,
                pedido,
            )
        }
    }
}
