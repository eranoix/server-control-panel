package com.vpsmanager.data.armazenamento

import java.io.File

/**
 * What the app occupies on the device, and what can be given back.
 *
 * ## Why this exists
 *
 * The owner asked for maintenance done "properly and automatically", and he
 * asked after a night when the terminal only came right once he had wiped the
 * app's storage by hand. That gesture is the symptom of an absence: when the
 * only maintenance tool is the operating system's button, any defect becomes
 * "wipe everything and hope", and the preferences, the configured server and
 * the session go out with the rubbish.
 *
 * ## The principle: each store declares its own policy
 *
 * There is no such thing as "clear the app". There are stores of different
 * natures, and treating them all alike is what makes a cleanup dangerous:
 *
 * - **Rebuildable and self-bounded** (the HTTP cache, media): it has its own
 *   ceiling and prunes itself. Maintenance need not touch it — only measure.
 *   Deleting it unprompted costs network on the next launch and gives back no
 *   space that was not already bounded.
 * - **Rebuildable and UNBOUNDED** (attachment and transfer temporaries): they
 *   grow forever if nobody sweeps. This is where maintenance earns its keep.
 * - **In transit** (an update downloaded and not yet installed): it is NOT
 *   rubbish until the install happens — deleting it undoes a download the
 *   person has already paid for. Only what is left over from past versions
 *   enters the sweep.
 * - **Irreplaceable** (preferences, credentials, the offline write queue):
 *   maintenance NEVER touches it. What is lost here does not come back with a
 *   network.
 *
 * This class is pure filesystem, with no `Context`, so it can be exercised on
 * the JVM — the policy is the part that needs testing, and it depends on no
 * Android at all.
 */
object ArmazenamentoDoApp {

    /**
     * The age at which a temporary becomes rubbish.
     *
     * Three days, and the number comes from what these files are: local copies
     * of something being uploaded. An upload that has not finished in three
     * days is not going to — either the file already went up, or the person
     * gave up. Shorter than that would risk deleting an upload paused by a trip
     * without a network.
     */
    const val DIAS_PARA_TEMPORARIO_VIRAR_LIXO = 3L

    private const val UM_DIA_MS = 24L * 60 * 60 * 1000

    /** The store's nature — it is what decides what maintenance may do. */
    enum class Natureza {
        /** It has its own ceiling and prunes itself. Maintenance only measures. */
        AUTO_LIMITADO,

        /** It grows forever if nobody sweeps. Maintenance sweeps by age. */
        TEMPORARIO,

        /** Only what is left over from past versions is rubbish. */
        EM_TRANSITO,
    }

    data class Deposito(
        val nome: String,
        val explicacao: String,
        val dir: File,
        val natureza: Natureza,
    )

    data class Uso(val nome: String, val explicacao: String, val bytes: Long, val podeLimpar: Boolean)

    data class Resultado(val liberadoBytes: Long, val arquivosRemovidos: Int)

    /** The recursive sum of what a directory occupies. A missing directory = 0, never an error. */
    fun tamanho(dir: File): Long {
        if (!dir.exists()) return 0
        if (dir.isFile) return dir.length()
        return dir.walkBottomUp().filter { it.isFile }.sumOf { it.length() }
    }

    fun medir(depositos: List<Deposito>): List<Uso> = depositos.map {
        Uso(
            nome = it.nome,
            explicacao = it.explicacao,
            bytes = tamanho(it.dir),
            // "Can be cleared" is about the BUTTON, not about the routine: a
            // self-bounded store needs no sweeping, but whoever is out of space
            // today has the right to say wipe it anyway.
            podeLimpar = true,
        )
    }

    /**
     * The routine. It only touches what the store's nature authorises.
     *
     * @param agoraMs an injectable clock — the age rule is the thing to test.
     * @param emUso files an operation in flight still needs (the update being
     *   downloaded). Never removed.
     */
    fun manutencao(
        depositos: List<Deposito>,
        agoraMs: Long,
        emUso: Set<File> = emptySet(),
    ): Resultado {
        var liberado = 0L
        var removidos = 0
        val limite = agoraMs - DIAS_PARA_TEMPORARIO_VIRAR_LIXO * UM_DIA_MS

        for (deposito in depositos) {
            // AUTO_LIMITADO is deliberately left out — see the class KDoc.
            if (deposito.natureza == Natureza.AUTO_LIMITADO) continue
            if (!deposito.dir.isDirectory) continue

            for (arquivo in deposito.dir.walkBottomUp()) {
                if (!arquivo.isFile) continue
                if (arquivo in emUso) continue
                val velho = arquivo.lastModified() in 1 until limite
                val lixo = when (deposito.natureza) {
                    Natureza.TEMPORARIO -> velho
                    // In transit: the criterion is not age, it is NOT BEING IN
                    // USE. An artifact from an already-installed version does
                    // not appear in `emUso` and goes on the first pass, without
                    // waiting three days while occupying tens of MB.
                    Natureza.EM_TRANSITO -> true
                    Natureza.AUTO_LIMITADO -> false
                }
                if (!lixo) continue
                val tamanho = arquivo.length()
                if (arquivo.delete()) {
                    liberado += tamanho
                    removidos++
                }
            }
        }
        return Resultado(liberadoBytes = liberado, arquivosRemovidos = removidos)
    }

    /** "12.4 MB" — for the screen. Base 1000, which is what Android uses on its own screens. */
    fun formatar(bytes: Long): String = when {
        bytes < 1_000 -> "$bytes B"
        bytes < 1_000_000 -> String.format("%.1f kB", bytes / 1_000.0)
        bytes < 1_000_000_000 -> String.format("%.1f MB", bytes / 1_000_000.0)
        else -> String.format("%.2f GB", bytes / 1_000_000_000.0)
    }
}
