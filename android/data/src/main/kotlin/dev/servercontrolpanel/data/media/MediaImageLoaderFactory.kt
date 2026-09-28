package dev.servercontrolpanel.data.media

import android.content.Context
import coil.ImageLoader
import coil.decode.VideoFrameDecoder
import coil.disk.DiskCache
import java.io.File

fun createMediaImageLoader(context: Context, cacheDirectory: File, maxCacheBytes: Long): ImageLoader =
    ImageLoader.Builder(context.applicationContext)
        .callFactory(::mediaCallFactory)
        .components { add(VideoFrameDecoder.Factory()) }
        .diskCache {
            DiskCache.Builder()
                .directory(cacheDirectory)
                .maxSizeBytes(maxCacheBytes)
                .build()
        }
        .build()
