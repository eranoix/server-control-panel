package com.vpsmanager.feature.whatsapp.media

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.media3.exoplayer.ExoPlayer
import coil.ImageLoader
import coil.compose.SubcomposeAsyncImage
import coil.request.ImageRequest
import com.vpsmanager.core.model.WhatsAppMessage
import kotlinx.coroutines.launch

/**
 * Dispatches a [WhatsAppMessage] with non-null `media` to the right
 * rendering per [WhatsAppMessage.type] -- the four categories the BFF already
 * emits (`image`/`video`/`audio`/`document`, mirroring
 * `internal/whatsapp`'s `guessMsgType`). Replaces the earlier
 * `"[mídia]"` placeholder row.
 *
 * [imageLoader] and [dataSourceFactory] are built once per conversation
 * screen (see `ConversationScreen`'s `remember`) and threaded down here
 * rather than re-resolved per row -- both own real resources (an HTTP
 * connection pool, a disk-cache file lock) that must not be recreated per
 * bubble. [sharedPlayer] is the single [ExoPlayer] every [AudioPlayerBar] in
 * this conversation shares, so starting one voice note stops whichever one
 * was already playing.
 */
@Composable
fun MediaMessageRow(
    message: WhatsAppMessage,
    serverBaseUrl: String?,
    imageLoader: ImageLoader,
    dataSourceFactory: androidx.media3.datasource.DataSource.Factory,
    sharedPlayer: ExoPlayer,
    onOpenImage: (url: String) -> Unit,
    onOpenVideo: (url: String) -> Unit,
) {
    val media = message.media ?: return
    val absoluteUrl = MediaCache.resolveUrl(serverBaseUrl, media.url)

    when (message.type) {
        "image" -> ImageThumbnail(
            url = absoluteUrl,
            imageLoader = imageLoader,
            onClick = { onOpenImage(absoluteUrl) },
        )
        "video" -> VideoThumbnail(
            url = absoluteUrl,
            imageLoader = imageLoader,
            onClick = { onOpenVideo(absoluteUrl) },
        )
        "audio" -> AudioPlayerBar(
            url = absoluteUrl,
            messageId = message.id,
            initialDurationSeconds = media.duration,
            player = sharedPlayer,
        )
        "document" -> DocumentCard(
            url = absoluteUrl,
            filename = media.filename ?: "file",
            mimeType = media.mimeType,
            sizeBytes = media.size,
            imageLoader = imageLoader,
        )
        else -> Text(
            text = "[unsupported media]",
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ImageThumbnail(url: String, imageLoader: ImageLoader, onClick: () -> Unit) {
    // SubcomposeAsyncImage (not AsyncImage) so the first, not-yet-cached
    // request shows a spinner instead of a blank box while Coil fetches the
    // bytes over the network -- the whole point of this row is showing real
    // media in place of the old "[mídia]" text, so a silent blank area during
    // that first download would look like nothing happened.
    SubcomposeAsyncImage(
        model = ImageRequest.Builder(androidx.compose.ui.platform.LocalContext.current)
            .data(url)
            .diskCacheKey(url)
            .build(),
        imageLoader = imageLoader,
        contentDescription = "Image",
        contentScale = ContentScale.Crop,
        loading = { ThumbnailLoadingIndicator() },
        error = { ThumbnailErrorIndicator() },
        modifier = Modifier.size(220.dp).clickable(onClick = onClick),
    )
}

@Composable
private fun VideoThumbnail(url: String, imageLoader: ImageLoader, onClick: () -> Unit) {
    Box(modifier = Modifier.size(220.dp).clickable(onClick = onClick), contentAlignment = Alignment.Center) {
        SubcomposeAsyncImage(
            model = ImageRequest.Builder(androidx.compose.ui.platform.LocalContext.current)
                .data(url)
                .diskCacheKey(url)
                .build(),
            imageLoader = imageLoader,
            contentDescription = "Video",
            contentScale = ContentScale.Crop,
            loading = { ThumbnailLoadingIndicator() },
            error = { ThumbnailErrorIndicator() },
            modifier = Modifier.size(220.dp),
        )
        Box(
            modifier = Modifier.size(48.dp).background(Color.Black.copy(alpha = 0.5f), CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Text(text = "▶", color = Color.White)
        }
    }
}

@Composable
private fun ThumbnailLoadingIndicator() {
    Box(modifier = Modifier.size(220.dp), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ThumbnailErrorIndicator() {
    Box(modifier = Modifier.size(220.dp), contentAlignment = Alignment.Center) {
        Text(text = "Failed to load media", color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun DocumentCard(
    url: String,
    filename: String,
    mimeType: String?,
    sizeBytes: Long?,
    imageLoader: ImageLoader,
) {
    val context = androidx.compose.ui.platform.LocalContext.current
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    var opening by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier
            .padding(4.dp)
            .clickable(enabled = !opening) {
                opening = true
                scope.launch {
                    DocumentOpener.open(
                        context = context,
                        imageLoader = imageLoader,
                        url = url,
                        filename = filename,
                        mimeType = mimeType,
                    )
                    opening = false
                }
            },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(text = "📄", modifier = Modifier.size(32.dp))
        Column(modifier = Modifier.padding(start = 8.dp)) {
            Text(text = filename)
            Text(
                text = formatSize(sizeBytes),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

private fun formatSize(bytes: Long?): String {
    if (bytes == null) return ""
    val kb = bytes / 1024.0
    return if (kb < 1024) {
        "%.0f KB".format(kb)
    } else {
        "%.1f MB".format(kb / 1024.0)
    }
}
