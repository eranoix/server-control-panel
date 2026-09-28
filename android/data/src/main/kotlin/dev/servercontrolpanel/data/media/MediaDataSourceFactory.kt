package dev.servercontrolpanel.data.media

import androidx.media3.datasource.DataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource

fun createMediaDataSourceFactory(): DataSource.Factory =
    OkHttpDataSource.Factory(mediaCallFactory())
