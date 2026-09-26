package com.vpsmanager.feature.whatsapp.media

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Button
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/**
 * Inline voice-note row. All rows in a conversation share one
 * [player] (built once in `ConversationScreen`) -- calling
 * [ExoPlayer.setMediaItem] for a newly tapped bubble naturally tears down
 * whatever the previous bubble had loaded, giving "only one voice note plays
 * at a time" for free with no extra bookkeeping beyond comparing
 * `player.currentMediaItem?.mediaId` to this row's own [messageId] to know
 * whether it is the currently active bubble.
 *
 * [player] streams through Media3's `OkHttpDataSource` (see
 * `MediaCache.dataSourceFactory`), which issues real HTTP Range requests as
 * the user scrubs -- confirmed by inspecting `OkHttpDataSource`'s decompiled
 * class file (adds a `Range: bytes=N-` header from its `DataSpec.position`),
 * not by an on-device playback run (no device/emulator available here).
 */
@Composable
fun AudioPlayerBar(
    url: String,
    messageId: String,
    initialDurationSeconds: Long?,
    player: ExoPlayer,
) {
    var isActiveRow by remember(messageId) { mutableStateOf(player.currentMediaItem?.mediaId == messageId) }
    var isPlaying by remember(messageId) { mutableStateOf(isActiveRow && player.isPlaying) }
    var positionMs by remember(messageId) { mutableFloatStateOf(0f) }
    var durationMs by remember(messageId) {
        mutableFloatStateOf(((initialDurationSeconds ?: 0L) * 1000).toFloat())
    }

    DisposableEffect(player, messageId) {
        val listener = object : Player.Listener {
            override fun onIsPlayingChanged(playing: Boolean) {
                if (player.currentMediaItem?.mediaId == messageId) isPlaying = playing
            }
            override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
                isActiveRow = mediaItem?.mediaId == messageId
                if (!isActiveRow) {
                    isPlaying = false
                    positionMs = 0f
                }
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }

    LaunchedEffectPolling(player, messageId, isPlaying) { pos, dur ->
        positionMs = pos
        if (dur > 0f) durationMs = dur
    }

    Row(modifier = Modifier.padding(4.dp), verticalAlignment = Alignment.CenterVertically) {
        Button(onClick = {
            if (player.currentMediaItem?.mediaId == messageId) {
                if (player.isPlaying) player.pause() else player.play()
            } else {
                player.setMediaItem(MediaItem.Builder().setUri(url).setMediaId(messageId).build())
                player.prepare()
                player.play()
            }
        }) {
            Text(if (isActiveRow && isPlaying) "Pause" else "Play")
        }
        Slider(
            value = if (durationMs > 0f) (positionMs / durationMs).coerceIn(0f, 1f) else 0f,
            onValueChange = { fraction ->
                if (player.currentMediaItem?.mediaId == messageId && durationMs > 0f) {
                    player.seekTo((fraction * durationMs).toLong())
                }
            },
            modifier = Modifier.width(140.dp).padding(horizontal = 8.dp),
        )
        Text(text = formatTime(positionMs) + " / " + formatTime(durationMs))
    }
}

@Composable
private fun LaunchedEffectPolling(
    player: ExoPlayer,
    messageId: String,
    isPlaying: Boolean,
    onTick: (positionMs: Float, durationMs: Float) -> Unit,
) {
    androidx.compose.runtime.LaunchedEffect(player, messageId, isPlaying) {
        while (isActive) {
            if (player.currentMediaItem?.mediaId == messageId) {
                val dur = player.duration
                onTick(player.currentPosition.toFloat(), if (dur > 0) dur.toFloat() else 0f)
            }
            delay(250)
        }
    }
}

private fun formatTime(ms: Float): String {
    val totalSeconds = (ms / 1000).toInt().coerceAtLeast(0)
    val minutes = totalSeconds / 60
    val seconds = totalSeconds % 60
    return "%d:%02d".format(minutes, seconds)
}
