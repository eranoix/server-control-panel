package dev.servercontrolpanel.feature.whatsapp.media

import android.content.Context
import androidx.media3.datasource.DataSource
import coil.ImageLoader
import dev.servercontrolpanel.data.media.createMediaDataSourceFactory
import dev.servercontrolpanel.data.media.createMediaImageLoader
import java.io.File

object MediaCache {

    const val MAX_CACHE_BYTES: Long = 256L * 1024 * 1024

    const val CACHE_SUBDIR: String = "whatsapp_media"

    fun directory(context: Context): File {
        val base = context.applicationContext.getExternalFilesDir(null)
            ?: context.applicationContext.filesDir
        return File(base, CACHE_SUBDIR).apply { mkdirs() }
    }

    fun imageLoader(context: Context): ImageLoader =
        createMediaImageLoader(
            context = context,
            cacheDirectory = directory(context),
            maxCacheBytes = MAX_CACHE_BYTES,
        )

    fun dataSourceFactory(): DataSource.Factory = createMediaDataSourceFactory()

    fun resolveUrl(serverBaseUrl: String?, relativeUrl: String): String =
        if (relativeUrl.contains("://")) {
            relativeUrl
        } else {
            serverBaseUrl.orEmpty() + relativeUrl
        }
}
