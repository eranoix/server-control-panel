package com.vpsmanager.feature.whatsapp.media

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.core.content.FileProvider
import coil.ImageLoader
import coil.annotation.ExperimentalCoilApi
import coil.request.ImageRequest

/**
 * Opens a document message in a system viewer app.
 *
 * Reuses [imageLoader] (the same Coil [ImageLoader] every image/video
 * thumbnail already downloads through) instead of adding a second download
 * mechanism: Coil's [coil.disk.DiskCache] persists the raw source bytes it
 * fetched to satisfy a request under [ImageRequest.Builder.diskCacheKey]
 * regardless of whether the bytes decode as a bitmap (verified by
 * decompiling `coil.disk.DiskCache$Snapshot` -- `getData()` returns an
 * [okio.Path] to those bytes directly, independent of decode outcome). A
 * document like a PDF or .docx never decodes as an image -- Coil's
 * `execute()` call below returns [coil.request.ErrorResult] for it every
 * time -- but the fetch stage still lands the file in the disk cache before
 * decode is attempted, so [ImageLoader.diskCache]'s `openSnapshot` still
 * finds it afterwards.
 */
object DocumentOpener {

    @OptIn(ExperimentalCoilApi::class)
    suspend fun open(
        context: Context,
        imageLoader: ImageLoader,
        url: String,
        filename: String,
        mimeType: String?,
    ) {
        val diskCache = imageLoader.diskCache
        val cacheKey = url

        // Warm the disk cache by attempting a decode -- for non-image
        // documents this always finishes as an ErrorResult, which is
        // expected and not itself an opening failure; what matters is
        // whether the bytes landed in the disk cache, checked below.
        val request = ImageRequest.Builder(context.applicationContext)
            .data(url)
            .diskCacheKey(cacheKey)
            .build()
        imageLoader.execute(request)

        val snapshot = diskCache?.openSnapshot(cacheKey)
        if (snapshot == null) {
            Toast.makeText(context, "Could not download $filename", Toast.LENGTH_SHORT).show()
            return
        }
        val cachedFile = try {
            snapshot.data.toFile()
        } finally {
            snapshot.close()
        }

        val authority = "${context.packageName}.whatsappmedia.fileprovider"
        val contentUri = FileProvider.getUriForFile(context, authority, cachedFile)
        val intent = buildViewIntent(contentUri, mimeType)

        val canHandle = intent.resolveActivity(context.packageManager) != null
        if (canHandle) {
            context.startActivity(intent)
        } else {
            Toast.makeText(context, "No installed app can open $filename", Toast.LENGTH_SHORT).show()
        }
    }

    /**
     * Extracted for [MediaCacheTest]-style unit coverage: asserts the
     * `content://` scheme and the revocable read-permission flag without
     * needing an installed viewer app or a device to resolve/launch against
     * (see the human verification script for the on-device open path).
     */
    internal fun buildViewIntent(contentUri: Uri, mimeType: String?): Intent =
        Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(contentUri, mimeType ?: "application/octet-stream")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
}
