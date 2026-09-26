package com.vpsmanager.feature.whatsapp

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.annotation.OptIn
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import com.vpsmanager.core.model.MessageSendStatus
import com.vpsmanager.core.model.WhatsAppMessage
import com.vpsmanager.data.config.EncryptedServerConfigStore
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.feature.whatsapp.media.ImageViewerScreen
import com.vpsmanager.feature.whatsapp.media.MediaCache
import com.vpsmanager.feature.whatsapp.media.MediaMessageRow
import com.vpsmanager.feature.whatsapp.media.VideoPlayerScreen
import com.vpsmanager.feature.whatsapp.send.AttachmentBar
import com.vpsmanager.feature.whatsapp.send.PickedAttachment
import com.vpsmanager.feature.whatsapp.send.UploadProgressBubble

/**
 * Renders [ConversationUiState] as it comes back from [ConversationViewModel]
 * -- history loaded once over REST, then kept live by
 * [ConversationViewModel]'s `/ws/whatsapp` subscription. Media messages
 * (image/video/audio/document) render via [MediaMessageRow] backed by the
 * bounded [MediaCache].
 *
 * [imageLoader], [dataSourceFactory] and [sharedPlayer] are each built once
 * per conversation via `remember`/`DisposableEffect`, scoped to this
 * screen's lifetime -- they own real resources (a disk-cache lock, an
 * ExoPlayer surface) that must not be recreated on every recomposition or
 * per message row. `serverBaseUrl` is resolved once the same way, mirroring
 * `feature-auth`'s existing call-site convention for `ServerConfigRepository`
 * (this app has no DI container).
 *
 * Building [sharedPlayer] with a [DefaultMediaSourceFactory] wrapping the
 * `:data`-supplied `DataSource.Factory` (Range-request-aware media3-okhttp
 * under the hood) touches a Media3 `@UnstableApi` surface. `UnstableApi` is
 * marked with `androidx.annotation.RequiresOptIn` (not Kotlin's
 * `kotlin.RequiresOptIn`), so it is opted into with `androidx.annotation.OptIn`
 * -- scoped to this function only, so the opt-in does not propagate to every
 * caller of [ConversationScreen] the way annotating the function itself
 * would.
 */
@OptIn(markerClass = [UnstableApi::class])
@Composable
fun ConversationScreen(
    modifier: Modifier = Modifier,
    viewModel: ConversationViewModel,
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val context = LocalContext.current

    val serverBaseUrl = remember {
        ServerConfigRepository(EncryptedServerConfigStore(context)).currentBaseUrl()
    }
    val imageLoader = remember { MediaCache.imageLoader(context) }
    val dataSourceFactory = remember { MediaCache.dataSourceFactory() }
    val sharedPlayer = remember {
        ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(dataSourceFactory))
            .build()
    }
    DisposableEffect(sharedPlayer) {
        onDispose { sharedPlayer.release() }
    }

    var fullScreenImageUrl by remember { mutableStateOf<String?>(null) }
    var fullScreenVideoUrl by remember { mutableStateOf<String?>(null) }

    Column(modifier = modifier.fillMaxSize()) {
        Box(modifier = Modifier.weight(1f)) {
            when (val current = state) {
                is ConversationUiState.Loading -> LoadingContent()
                is ConversationUiState.Error -> ErrorContent(message = current.message, onRetry = viewModel::retry)
                is ConversationUiState.Content -> ConversationContent(
                    state = current,
                    onRetrySend = viewModel::retrySend,
                    onRetryMediaSend = viewModel::retryMediaSend,
                    serverBaseUrl = serverBaseUrl,
                    imageLoader = imageLoader,
                    dataSourceFactory = dataSourceFactory,
                    sharedPlayer = sharedPlayer,
                    onOpenImage = { fullScreenImageUrl = it },
                    onOpenVideo = { fullScreenVideoUrl = it },
                )
            }

            fullScreenImageUrl?.let { url ->
                ImageViewerScreen(url = url, imageLoader = imageLoader, onClose = { fullScreenImageUrl = null })
            }
            fullScreenVideoUrl?.let { url ->
                VideoPlayerScreen(url = url, player = sharedPlayer, onClose = { fullScreenVideoUrl = null })
            }
        }
        Composer(
            enabled = (state as? ConversationUiState.Content)?.messages?.none {
                it.sendStatus == MessageSendStatus.SENDING
            } ?: false,
            onSend = viewModel::sendMessage,
            onAttachmentReady = viewModel::sendMedia,
        )
    }
}

@Composable
private fun LoadingContent() {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorContent(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(text = message, textAlign = TextAlign.Center)
            Button(onClick = onRetry, modifier = Modifier.padding(top = 16.dp)) {
                Text("Try again")
            }
        }
    }
}

@Composable
private fun ConversationContent(
    state: ConversationUiState.Content,
    onRetrySend: (String) -> Unit,
    onRetryMediaSend: (String) -> Unit,
    serverBaseUrl: String?,
    imageLoader: coil.ImageLoader,
    dataSourceFactory: androidx.media3.datasource.DataSource.Factory,
    sharedPlayer: ExoPlayer,
    onOpenImage: (String) -> Unit,
    onOpenVideo: (String) -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize()) {
        if (state.backfilling) {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
        }
        if (state.messages.isEmpty()) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(
                    text = "No messages yet.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        } else {
            LazyColumn(modifier = Modifier.fillMaxSize().padding(8.dp)) {
                items(state.messages, key = { it.id }) { message ->
                    MessageBubble(
                        message = message,
                        uploadState = message.clientMsgId?.let { state.uploads[it] },
                        onRetry = { onRetrySend(message.clientMsgId.orEmpty()) },
                        onRetryMediaSend = onRetryMediaSend,
                        serverBaseUrl = serverBaseUrl,
                        imageLoader = imageLoader,
                        dataSourceFactory = dataSourceFactory,
                        sharedPlayer = sharedPlayer,
                        onOpenImage = onOpenImage,
                        onOpenVideo = onOpenVideo,
                    )
                }
            }
        }
    }
}

@Composable
private fun MessageBubble(
    message: WhatsAppMessage,
    uploadState: MediaUploadState?,
    onRetry: () -> Unit,
    onRetryMediaSend: (String) -> Unit,
    serverBaseUrl: String?,
    imageLoader: coil.ImageLoader,
    dataSourceFactory: androidx.media3.datasource.DataSource.Factory,
    sharedPlayer: ExoPlayer,
    onOpenImage: (String) -> Unit,
    onOpenVideo: (String) -> Unit,
) {
    val alignment = if (message.fromMe) Alignment.End else Alignment.Start
    val bubbleColor = if (message.fromMe) {
        MaterialTheme.colorScheme.primaryContainer
    } else {
        MaterialTheme.colorScheme.surfaceVariant
    }

    Column(
        modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
        horizontalAlignment = alignment,
    ) {
        Surface(
            color = bubbleColor,
            shape = RoundedCornerShape(12.dp),
        ) {
            Column(modifier = Modifier.widthIn(max = 280.dp).padding(10.dp)) {
                if (uploadState != null) {
                    // Still local/in-flight -- UploadProgressBubble reads the
                    // `file://` path directly and owns its own progress/retry
                    // affordance, so this bubble does not fall through to the
                    // server-URL-based MediaMessageRow until it reconciles.
                    UploadProgressBubble(
                        message = message,
                        uploadState = uploadState,
                        imageLoader = imageLoader,
                        onRetry = onRetryMediaSend,
                    )
                } else if (message.media != null) {
                    MediaMessageRow(
                        message = message,
                        serverBaseUrl = serverBaseUrl,
                        imageLoader = imageLoader,
                        dataSourceFactory = dataSourceFactory,
                        sharedPlayer = sharedPlayer,
                        onOpenImage = onOpenImage,
                        onOpenVideo = onOpenVideo,
                    )
                } else {
                    Text(text = message.text.orEmpty())
                }
                if (message.reactions.isNotEmpty()) {
                    Text(
                        text = message.reactions.joinToString(" ") { it.emoji },
                        modifier = Modifier.padding(top = 4.dp),
                    )
                }
            }
        }
        if (uploadState == null) {
            // A media bubble already shows its own "Enviando.../Falha ao
            // enviar" state inline via UploadProgressBubble -- this row is
            // only for plain-text sends.
            when (message.sendStatus) {
                MessageSendStatus.SENDING -> Text(
                    text = "Sending…",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                MessageSendStatus.FAILED -> Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(text = "Failed to send", color = MaterialTheme.colorScheme.error)
                    IconButton(onClick = onRetry) {
                        Text("Try again")
                    }
                }
                // QUEUED: deliberately no retry button.
                //
                // The message is already stored and will go out on its own;
                // offering "try again" would invite the person to create a
                // second copy of the same message — which is exactly what the
                // queue exists to prevent. The sentence says what is going to
                // happen, and the right action is none.
                MessageSendStatus.NA_FILA -> Text(
                    text = "Queued — sends when the internet is back",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                MessageSendStatus.SENT -> Unit
            }
        }
    }
}

@Composable
private fun Composer(
    enabled: Boolean,
    onSend: (String) -> Unit,
    onAttachmentReady: (PickedAttachment) -> Unit,
) {
    var draft by remember { mutableStateOf("") }

    Row(
        modifier = Modifier.fillMaxWidth().padding(8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        AttachmentBar(enabled = enabled, onAttachmentReady = onAttachmentReady)
        OutlinedTextField(
            value = draft,
            onValueChange = { draft = it },
            modifier = Modifier.weight(1f),
            placeholder = { Text("Message") },
        )
        Button(
            enabled = enabled && draft.isNotBlank(),
            onClick = {
                onSend(draft)
                draft = ""
            },
        ) {
            Text("Send")
        }
    }
}
