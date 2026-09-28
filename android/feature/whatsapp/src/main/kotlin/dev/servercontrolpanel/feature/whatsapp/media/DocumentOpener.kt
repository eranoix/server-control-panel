package dev.servercontrolpanel.feature.whatsapp.media

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.core.content.FileProvider
import coil.ImageLoader
import coil.annotation.ExperimentalCoilApi
import coil.request.ImageRequest

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

    internal fun buildViewIntent(contentUri: Uri, mimeType: String?): Intent =
        Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(contentUri, mimeType ?: "application/octet-stream")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
}
