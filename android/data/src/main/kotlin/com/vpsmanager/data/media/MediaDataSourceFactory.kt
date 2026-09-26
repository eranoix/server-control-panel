package com.vpsmanager.data.media

import androidx.media3.datasource.DataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource

/**
 * The [DataSource.Factory] `:feature-whatsapp`'s ExoPlayer instances read
 * video/audio bytes through -- streamed with real HTTP Range requests
 * (ExoPlayer issues them itself for seeking; ExoPlayer's
 * `DefaultLoadControl`/progressive extractor never buffers the whole file
 * first). Built on `androidx.media3:media3-datasource-okhttp` wrapping
 * [mediaCallFactory] instead of Media3's own `DefaultHttpDataSource`
 * (`java.net.HttpURLConnection`-based) so playback shares the exact same
 * `OkHttpClient` connection pool, TLS/proxy config and bearer-auth
 * interceptor as every other BFF call -- not a second, independently
 * configured network stack.
 *
 * Returns Media3's own [DataSource.Factory] type, never `okhttp3.*` --
 * `:feature-whatsapp` wires this straight into `DefaultMediaSourceFactory`
 * without ever seeing the `Call.Factory` underneath.
 */
fun createMediaDataSourceFactory(): DataSource.Factory =
    OkHttpDataSource.Factory(mediaCallFactory())
