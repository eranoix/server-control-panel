package com.vpsmanager.data.storage

import android.content.Context
import com.vpsmanager.data.storage.AppStorage.StorageArea
import com.vpsmanager.data.storage.AppStorage.Kind
import java.io.File

/**
 * The REAL map of what this application keeps on the device.
 *
 * Separate from [AppStorage] because what lives here is the only thing
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
object DeviceStorageAreas {

    /** `ReadCache.instalar` creates this one. Its own 24 MiB ceiling. */
    private const val CACHE_HTTP = "bff-http"

    /** `MediaCache.CACHE_SUBDIR`. Its own 256 MiB ceiling, pruned by Coil. */
    private const val WHATSAPP_MEDIA = "whatsapp_media"

    /** `AttachmentSourceSheet.PHOTOS_DIR` — fotos tiradas para anexar. */
    private const val TERMINAL_ATTACHMENTS = "anexos-terminal"

    /** `UpdateStaging.DIR_NAME` — what is being downloaded, and what was left behind. */
    private const val UPDATES = "atualizacoes"

    fun from(context: Context): List<StorageArea> {
        val app = context.applicationContext
        val cache = app.cacheDir
        val external = app.getExternalFilesDir(null) ?: app.filesDir
        return listOf(
            StorageArea(
                name = "Read cache",
                explanation = "Server responses that keep the app usable without internet",
                dir = File(cache, CACHE_HTTP),
                kind = Kind.AUTO_BOUNDED,
            ),
            StorageArea(
                name = "WhatsApp media",
                explanation = "Photos, videos and audio already downloaded from chats",
                dir = File(external, WHATSAPP_MEDIA),
                kind = Kind.AUTO_BOUNDED,
            ),
            StorageArea(
                name = "Terminal attachments",
                explanation = "Local copies of what was sent to the session",
                dir = File(cache, TERMINAL_ATTACHMENTS),
                kind = Kind.TEMPORARY,
            ),
            StorageArea(
                name = "Downloaded updates",
                explanation = "The package for the next version; older ones are discarded",
                dir = File(app.filesDir, UPDATES),
                kind = Kind.IN_TRANSIT,
            ),
        )
    }

    fun measure(context: Context): List<AppStorage.Usage> =
        AppStorage.measure(from(context))

    /**
     * The device's routine sweep.
     *
     * [inUse] is what an operation in flight still needs — today, the artifact
     * of the update being downloaded. The caller knows that; this layer does
     * not guess.
     */
    fun maintenance(context: Context, inUse: Set<File> = emptySet()): AppStorage.CleanupResult =
        AppStorage.maintenance(
            areas = from(context),
            nowMs = System.currentTimeMillis(),
            inUse = inUse,
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
    fun clearAllRebuildable(context: Context, inUse: Set<File> = emptySet()): AppStorage.CleanupResult {
        var freed = 0L
        var removed = 0
        for (area in from(context)) {
            if (!area.dir.isDirectory) continue
            for (file in area.dir.walkBottomUp()) {
                if (!file.isFile || file in inUse) continue
                val size = file.length()
                if (file.delete()) {
                    freed += size
                    removed++
                }
            }
        }
        return AppStorage.CleanupResult(freed, removed)
    }
}
