package com.vpsmanager.feature.whatsapp.media

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView

/**
 * The full-screen video player -- opened when [MediaMessageRow] handles
 * a tap on a `video`-type bubble's thumbnail. Reuses the conversation's
 * single [player] (the same [ExoPlayer] instance [AudioPlayerBar] rows
 * share), so opening a video pauses whatever voice note was playing, and
 * closing this screen leaves the player idle rather than tearing it down --
 * it is released once, at [com.vpsmanager.feature.whatsapp.ConversationScreen]'s
 * own `DisposableEffect`.
 *
 * The [player] is fed through Media3's `OkHttpDataSource` (see
 * `MediaCache.dataSourceFactory`), which issues genuine byte-range requests
 * as the user seeks -- verified only by decompiling `OkHttpDataSource`'s
 * class file (adds a `Range: bytes=N-` header derived from the seek
 * position), NOT by an on-device playback run (no device/emulator available
 * here; see the human verification script).
 */
@Composable
fun VideoPlayerScreen(
    url: String,
    player: ExoPlayer,
    onClose: () -> Unit,
) {
    DisposableEffect(url) {
        player.setMediaItem(MediaItem.Builder().setUri(url).setMediaId(url).build())
        player.prepare()
        player.play()
        onDispose { player.pause() }
    }

    Box(modifier = Modifier.fillMaxSize().background(Color.Black)) {
        AndroidView(
            factory = { context ->
                PlayerView(context).apply { this.player = player }
            },
            modifier = Modifier.fillMaxSize(),
        )
        Text(
            text = "Close",
            color = Color.White,
            modifier = Modifier
                .align(Alignment.TopEnd)
                .clickable(onClick = onClose)
                .background(Color.Black.copy(alpha = 0.5f))
                .padding(12.dp),
        )
    }
}
