package com.vpsmanager.data.media

import android.content.Context
import coil.ImageLoader
import coil.decode.VideoFrameDecoder
import coil.disk.DiskCache
import java.io.File

/**
 * Builds the one [ImageLoader] `:feature-whatsapp` uses for image
 * thumbnails/full-screen viewing and video-frame thumbnails. Everything that
 * touches `okhttp3.*` (the [mediaCallFactory] wiring) lives in this function,
 * inside `:data` -- callers outside this module only ever see the finished
 * [ImageLoader], never the `Call.Factory` that built it.
 *
 * [VideoFrameDecoder.Factory] is registered so a video-MIME message renders a
 * decoded frame the exact same way an image-MIME message renders its bytes --
 * one dispatcher path, one cache, no separate video-thumbnail mechanism.
 *
 * The disk cache is capped at exactly [maxCacheBytes] (not a percentage of
 * free space) with Coil's built-in LRU eviction -- this function's only job
 * regarding size is setting that cap and pointing [cacheDirectory] at the
 * caller-chosen folder; eviction itself is Coil's `DiskLruCache`-backed
 * default. A single cached item larger than [maxCacheBytes] is never written
 * to disk (Coil's `DiskLruCache` refuses an entry that cannot fit even after
 * evicting everything else) -- Coil still streams it through decode/playback
 * once per request, just without ever landing on disk.
 */
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
