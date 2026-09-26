package com.vpsmanager.data.armazenamento

import android.content.Context
import com.vpsmanager.data.armazenamento.ArmazenamentoDoApp.Deposito
import com.vpsmanager.data.armazenamento.ArmazenamentoDoApp.Natureza
import java.io.File

/**
 * The REAL map of what this application keeps on the device.
 *
 * Separate from [ArmazenamentoDoApp] because what lives here is the only thing
 * that needs a `Context` — the paths. The policy, which is the part capable of
 * deleting something it should not, lives over there, tested on the JVM.
 *
 * ## Keeping this list complete is the work
 *
 * A store nobody listed is neither measured nor swept, and grows in silence
 * until the person sees the application taking a gigabyte in the system
 * settings. When creating a new directory under `cacheDir` or `filesDir`, **add
 * it here** — it is cheap, and it is the difference between maintenance and
 * theatre.
 *
 * The subdirectory names are duplicated on purpose instead of imported from the
 * owning modules: `:data` cannot depend on `:feature-whatsapp` or on
 * `:feature-terminal` (the arrow points the other way). The alternative would be
 * a dynamic registry each feature fills at startup — more indirection than the
 * problem calls for, and with the new risk of a store vanishing from the list
 * because some module was never initialised.
 */
object DepositosDoAparelho {

    /** `CacheDeLeitura.instalar` creates this one. Its own 24 MiB ceiling. */
    private const val CACHE_HTTP = "bff-http"

    /** `MediaCache.CACHE_SUBDIR`. Its own 256 MiB ceiling, pruned by Coil. */
    private const val MIDIA_WHATSAPP = "whatsapp_media"

    /** `FolhaDeOrigemDoAnexo.PASTA_DE_FOTOS` — fotos tiradas para anexar. */
    private const val ANEXOS_DO_TERMINAL = "anexos-terminal"

    /** `UpdateStaging.DIR_NAME` — what is being downloaded, and what was left behind. */
    private const val ATUALIZACOES = "atualizacoes"

    fun de(context: Context): List<Deposito> {
        val app = context.applicationContext
        val cache = app.cacheDir
        val externo = app.getExternalFilesDir(null) ?: app.filesDir
        return listOf(
            Deposito(
                nome = "Read cache",
                explicacao = "Server responses that keep the app usable without internet",
                dir = File(cache, CACHE_HTTP),
                natureza = Natureza.AUTO_LIMITADO,
            ),
            Deposito(
                nome = "WhatsApp media",
                explicacao = "Photos, videos and audio already downloaded from chats",
                dir = File(externo, MIDIA_WHATSAPP),
                natureza = Natureza.AUTO_LIMITADO,
            ),
            Deposito(
                nome = "Terminal attachments",
                explicacao = "Local copies of what was sent to the session",
                dir = File(cache, ANEXOS_DO_TERMINAL),
                natureza = Natureza.TEMPORARIO,
            ),
            Deposito(
                nome = "Downloaded updates",
                explicacao = "The package for the next version; older ones are discarded",
                dir = File(app.filesDir, ATUALIZACOES),
                natureza = Natureza.EM_TRANSITO,
            ),
        )
    }

    fun medir(context: Context): List<ArmazenamentoDoApp.Uso> =
        ArmazenamentoDoApp.medir(de(context))

    /**
     * The device's routine sweep.
     *
     * [emUso] is what an operation in flight still needs — today, the artifact
     * of the update being downloaded. The caller knows that; this layer does
     * not guess.
     */
    fun manutencao(context: Context, emUso: Set<File> = emptySet()): ArmazenamentoDoApp.Resultado =
        ArmazenamentoDoApp.manutencao(
            depositos = de(context),
            agoraMs = System.currentTimeMillis(),
            emUso = emUso,
        )

    /**
     * The "free up space now" button: deletes EVERYTHING that can be rebuilt,
     * including what prunes itself.
     *
     * This is not the routine — it is the explicit request of someone out of
     * space on the device today who accepts paying in network traffic later. It
     * still leaves untouched what is irreplaceable (preferences, credentials,
     * the offline write queue), and it is that boundary that separates this
     * from "clear the app's storage" in the system settings.
     */
    fun limparTudoReconstruivel(context: Context, emUso: Set<File> = emptySet()): ArmazenamentoDoApp.Resultado {
        var liberado = 0L
        var removidos = 0
        for (deposito in de(context)) {
            if (!deposito.dir.isDirectory) continue
            for (arquivo in deposito.dir.walkBottomUp()) {
                if (!arquivo.isFile || arquivo in emUso) continue
                val tamanho = arquivo.length()
                if (arquivo.delete()) {
                    liberado += tamanho
                    removidos++
                }
            }
        }
        return ArmazenamentoDoApp.Resultado(liberado, removidos)
    }
}
