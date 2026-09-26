package com.vpsmanager.data.storage

import android.content.Context
import com.vpsmanager.data.storage.AppStorage.StorageArea
import com.vpsmanager.data.storage.AppStorage.Kind
import java.io.File

/**
 * The real map of what this app keeps on the device (the paths, which need a
 * `Context`); the deletion policy lives in [AppStorage], tested on the JVM.
 *
 * A store that is not listed here is neither measured nor swept, so add every new
 * directory under `cacheDir` or `filesDir`. Subdirectory names are duplicated
 * rather than imported because `:data` cannot depend on the feature modules.
 */
object DeviceStorageAreas {

    /** Created by `ReadCache.install`, with its own 24 MiB ceiling. */
    private const val CACHE_HTTP = "bff-http"

    /** `MediaCache.CACHE_SUBDIR`, with its own 256 MiB ceiling pruned by Coil. */
    private const val WHATSAPP_MEDIA = "whatsapp_media"

    /** `AttachmentSourceSheet.PHOTOS_DIR`: photos taken to attach. */
    private const val TERMINAL_ATTACHMENTS = "anexos-terminal"

    /** `UpdateStaging.DIR_NAME`: the download in progress and leftovers. */
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
     * The routine sweep. [inUse] lists what an operation in flight still needs
     * (e.g. the update artifact being downloaded); the caller knows, this layer does not guess.
     */
    fun maintenance(context: Context, inUse: Set<File> = emptySet()): AppStorage.CleanupResult =
        AppStorage.maintenance(
            areas = from(context),
            nowMs = System.currentTimeMillis(),
            inUse = inUse,
        )

    /**
     * The "free up space now" button: deletes everything rebuildable, including
     * self-pruning caches. Irreplaceable data (preferences, credentials, the
     * offline write queue) is never touched.
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
