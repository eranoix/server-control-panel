package dev.servercontrolpanel.feature.whatsapp.send

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import coil.ImageLoader
import coil.compose.SubcomposeAsyncImage
import coil.request.ImageRequest
import dev.servercontrolpanel.core.model.WhatsAppMessage
import dev.servercontrolpanel.feature.whatsapp.ConversationUiState
import dev.servercontrolpanel.feature.whatsapp.MediaUploadState

@Composable
fun UploadProgressBubble(
    message: WhatsAppMessage,
    uploadState: MediaUploadState,
    imageLoader: ImageLoader,
    onRetry: (clientMsgId: String) -> Unit,
) {
    val media = message.media ?: return
    val clientMsgId = message.clientMsgId ?: return

    Column(modifier = Modifier.padding(4.dp)) {
        if (message.type == "image") {
            SubcomposeAsyncImage(
                model = ImageRequest.Builder(androidx.compose.ui.platform.LocalContext.current)
                    .data(java.io.File(java.net.URI(media.url)))
                    .build(),
                imageLoader = imageLoader,
                contentDescription = "Image being uploaded",
                contentScale = ContentScale.Crop,
                modifier = Modifier.size(220.dp),
            )
        } else {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(text = attachmentIcon(message.type), modifier = Modifier.size(32.dp))
                Text(text = media.filename ?: "file", modifier = Modifier.padding(start = 8.dp))
            }
        }

        when (uploadState) {
            is MediaUploadState.InProgress -> {
                LinearProgressIndicator(
                    progress = { uploadState.percent / 100f },
                    modifier = Modifier.padding(top = 4.dp),
                )
                Text(
                    text = "Uploading... ${uploadState.percent}%",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            is MediaUploadState.Failed -> {
                Text(
                    text = uploadState.reason,
                    color = MaterialTheme.colorScheme.error,
                )
                if (uploadState.retryable) {
                    TextButton(onClick = { onRetry(clientMsgId) }) {
                        Text(text = "Try again")
                    }
                }
            }
        }
    }
}

private fun attachmentIcon(msgType: String): String = when (msgType) {
    "video" -> "🎥"
    "audio" -> "🎤"
    else -> "📄"
}
