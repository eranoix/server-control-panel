package dev.servercontrolpanel.feature.whatsapp.media

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
