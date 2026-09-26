package com.vpsmanager.feature.whatsapp.media

import android.content.Context
import androidx.media3.datasource.DataSource
import coil.ImageLoader
import com.vpsmanager.data.media.createMediaDataSourceFactory
import com.vpsmanager.data.media.createMediaImageLoader
import java.io.File

/**
 * The bounded local cache for WhatsApp media (image/video thumbnails and
 * full images downloaded on demand through the BFF media route). This object
 * owns the *policy* -- how big the cache is allowed to get and which folder
 * it lives in -- while [com.vpsmanager.data.media.createMediaImageLoader]
 * owns the *mechanism* (Coil's `ImageLoader`/`DiskCache`, wired to the app's
 * authenticated OkHttp client). The split exists because `:feature-whatsapp`
 * may never construct an `okhttp3.*` type directly; everything that does
 * lives in `:data`.
 *
 * The cap is a fixed 256 MiB rather than a percentage of free disk space --
 * a judgement call, since nothing upstream pins a number. 256 MiB
 * comfortably holds a very chatty conversation's worth of photos/video-
 * thumbnail frames without risking a meaningful dent in a phone's storage;
 * once the eviction runs (Coil's default LRU, see [createMediaImageLoader]),
 * the oldest entries are dropped first. Bumping this constant is the only
 * change needed to retune the budget.
 *
 * The cache lives under the app's private *external* files directory (falls
 * back to internal storage if external is unavailable, e.g. no SD card
 * mounted) in a single named subfolder -- the same subfolder
 * `whatsapp_file_paths.xml` grants [DocumentOpener] a scoped `FileProvider`
 * read into, and nothing outside it.
 */
object MediaCache {

    /** 256 MiB -- see class doc for the reasoning behind this exact number. */
    const val MAX_CACHE_BYTES: Long = 256L * 1024 * 1024

    const val CACHE_SUBDIR: String = "whatsapp_media"

    /** Where cached media bytes (thumbnails, full images, downloaded documents) live on disk. */
    fun directory(context: Context): File {
        val base = context.applicationContext.getExternalFilesDir(null)
            ?: context.applicationContext.filesDir
        return File(base, CACHE_SUBDIR).apply { mkdirs() }
    }

    /**
     * The one [ImageLoader] every image/video thumbnail and the full-screen
     * image viewer use. Not memoized here -- callers (see
     * `MediaMessageRow`/`ConversationScreen`) hold it in a Compose `remember`
     * scoped to the conversation screen's lifetime, which already gives it
     * the single-instance behavior an `ImageLoader` (its own internal
     * connection pool, its own disk cache lock) needs.
     */
    fun imageLoader(context: Context): ImageLoader =
        createMediaImageLoader(
            context = context,
            cacheDirectory = directory(context),
            maxCacheBytes = MAX_CACHE_BYTES,
        )

    /** The Media3 [DataSource.Factory] every ExoPlayer instance (video, audio) reads through. */
    fun dataSourceFactory(): DataSource.Factory = createMediaDataSourceFactory()

    /**
     * [relativeUrl] is `MessageView.media.url` as the BFF returns it --
     * always a path rooted at `/api/mobile/v1/...`, never an absolute URL.
     * Coil/Media3 need a real absolute URL to issue an HTTP request, so this
     * prefixes the configured server origin ([serverBaseUrl], from
     * `ServerConfigRepository.currentBaseUrl()`).
     * Passes an already-absolute value through unchanged -- originally just
     * `http(s)://` (defensive: nothing emitted one, but a future BFF change
     * safely wouldn't double up), later generalized to any URI scheme so a
     * `file://` path for a not-yet-uploaded outgoing attachment (see
     * `UploadProgressBubble`/`ConversationViewModel`'s optimistic media
     * bubble) also passes through untouched instead of getting a server
     * origin wrongly prepended to a local path.
     * A null [serverBaseUrl] (device never configured/paired) yields a
     * still-relative string that fails loud when Coil/Media3 try to resolve
     * it as a URL -- the same "fails loud, never guesses localhost" posture
     * as every other repository in `:data`.
     */
    fun resolveUrl(serverBaseUrl: String?, relativeUrl: String): String =
        if (relativeUrl.contains("://")) {
            relativeUrl
        } else {
            serverBaseUrl.orEmpty() + relativeUrl
        }
}
