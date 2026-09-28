package dev.servercontrolpanel.feature.whatsapp

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.core.model.MessageSendStatus
import dev.servercontrolpanel.core.model.WhatsAppMedia
import dev.servercontrolpanel.core.model.WhatsAppMessage
import dev.servercontrolpanel.core.model.WhatsAppReaction
import dev.servercontrolpanel.data.whatsapp.MessagesResult
import dev.servercontrolpanel.data.whatsapp.SendResult
import dev.servercontrolpanel.data.whatsapp.WhatsAppRepository
import dev.servercontrolpanel.data.whatsapp.WhatsAppWsEvent
import dev.servercontrolpanel.feature.whatsapp.send.MediaSendOutcome
import dev.servercontrolpanel.feature.whatsapp.send.MediaUploadWorker
import dev.servercontrolpanel.feature.whatsapp.send.PickedAttachment
import dev.servercontrolpanel.feature.whatsapp.send.clampUploadProgress
import java.io.File
import java.net.URI
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface MediaUploadState {
    data class InProgress(val percent: Int) : MediaUploadState
    data class Failed(val reason: String, val retryable: Boolean) : MediaUploadState
}

sealed interface ConversationUiState {
    data object Loading : ConversationUiState
    data class Error(val message: String) : ConversationUiState
    data class Content(
        val messages: List<WhatsAppMessage>,
        val backfilling: Boolean,
        val uploads: Map<String, MediaUploadState> = emptyMap(),
    ) : ConversationUiState
}

open class ConversationViewModel(
    private val jid: String,
    private val repository: WhatsAppRepository = WhatsAppRepository(),
    private val eventSource: WhatsAppEventSource,
    private val mediaUploadWorker: MediaUploadWorker = MediaUploadWorker(repository),
) : ViewModel() {

    private val _uiState = MutableStateFlow<ConversationUiState>(ConversationUiState.Loading)
    val uiState: StateFlow<ConversationUiState> = _uiState.asStateFlow()

    private var hasConnectedOnce = false

    init {
        load()
        observeConnectionState()
        observeEvents()
        eventSource.connect()
    }

    override fun onCleared() {
        eventSource.disconnect()
    }

    fun retry() = load()

    fun sendMessage(text: String) {
        if (text.isBlank()) return
        sendWithId(clientMsgId = UUID.randomUUID().toString(), text = text)
    }

    fun retrySend(clientMsgId: String) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        val failed = current.messages.firstOrNull { it.clientMsgId == clientMsgId } ?: return
        sendWithId(clientMsgId = clientMsgId, text = failed.text.orEmpty())
    }

    private fun sendWithId(clientMsgId: String, text: String) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        val alreadyPending = current.messages.any { it.clientMsgId == clientMsgId }
        val optimistic = WhatsAppMessage(
            id = "pending:$clientMsgId",
            chatJid = jid,
            fromMe = true,
            sender = null,
            text = text,
            type = "text",
            ts = System.currentTimeMillis() / 1000,
            ack = 0,
            quotedId = null,
            media = null,
            reactions = emptyList(),
            clientMsgId = clientMsgId,
            sendStatus = MessageSendStatus.SENDING,
        )
        _uiState.value = current.copy(
            messages = if (alreadyPending) {
                current.messages.map { if (it.clientMsgId == clientMsgId) optimistic else it }
            } else {
                current.messages + optimistic
            },
        )

        viewModelScope.launch {
            when (val result = repository.sendMessage(jid = jid, clientMsgId = clientMsgId, text = text)) {
                is SendResult.Success -> reconcileSent(clientMsgId, result.id)
                is SendResult.Queued -> markQueued(clientMsgId)
                is SendResult.Error -> markFailed(clientMsgId)
            }
        }
    }

    private fun reconcileSent(clientMsgId: String, serverId: String) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        if (current.messages.any { it.id == serverId }) return
        _uiState.value = current.copy(
            messages = current.messages.map { msg ->
                if (msg.clientMsgId == clientMsgId) {
                    msg.copy(id = serverId, sendStatus = MessageSendStatus.SENT)
                } else {
                    msg
                }
            },
        )
    }

    private fun markQueued(clientMsgId: String) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        _uiState.value = current.copy(
            messages = current.messages.map { msg ->
                if (msg.clientMsgId == clientMsgId) {
                    msg.copy(sendStatus = MessageSendStatus.QUEUED)
                } else {
                    msg
                }
            },
        )
    }

    private fun markFailed(clientMsgId: String) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        _uiState.value = current.copy(
            messages = current.messages.map { msg ->
                if (msg.clientMsgId == clientMsgId) msg.copy(sendStatus = MessageSendStatus.FAILED) else msg
            },
        )
    }

    fun sendMedia(attachment: PickedAttachment, caption: String? = null, quotedId: String? = null) {
        sendMediaWithId(clientMsgId = UUID.randomUUID().toString(), attachment = attachment, caption = caption, quotedId = quotedId)
    }

    fun retryMediaSend(clientMsgId: String) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        val failed = current.messages.firstOrNull { it.clientMsgId == clientMsgId } ?: return
        val media = failed.media ?: return
        val attachment = PickedAttachment(
            file = mediaFileFromUrl(media.url),
            filename = media.filename ?: "file",
            mimeType = media.mimeType,
            sizeBytes = media.size ?: 0L,
            msgType = failed.type,
        )
        sendMediaWithId(clientMsgId = clientMsgId, attachment = attachment, caption = failed.text, quotedId = failed.quotedId)
    }

    private fun sendMediaWithId(clientMsgId: String, attachment: PickedAttachment, caption: String?, quotedId: String?) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        val alreadyPending = current.messages.any { it.clientMsgId == clientMsgId }
        val optimistic = WhatsAppMessage(
            id = "pending:$clientMsgId",
            chatJid = jid,
            fromMe = true,
            sender = null,
            text = caption,
            type = attachment.msgType,
            ts = System.currentTimeMillis() / 1000,
            ack = 0,
            quotedId = quotedId,
            media = WhatsAppMedia(
                url = attachment.file.toURI().toString(),
                mimeType = attachment.mimeType,
                filename = attachment.filename,
                size = attachment.sizeBytes,
                duration = null,
                width = null,
                height = null,
            ),
            reactions = emptyList(),
            clientMsgId = clientMsgId,
            sendStatus = MessageSendStatus.SENDING,
        )
        _uiState.value = current.copy(
            messages = if (alreadyPending) {
                current.messages.map { if (it.clientMsgId == clientMsgId) optimistic else it }
            } else {
                current.messages + optimistic
            },
            uploads = current.uploads + (clientMsgId to MediaUploadState.InProgress(0)),
        )

        viewModelScope.launch {
            when (
                val outcome = mediaUploadWorker.upload(
                    jid = jid,
                    clientMsgId = clientMsgId,
                    file = attachment.file,
                    sizeBytes = attachment.sizeBytes,
                    mimeType = attachment.mimeType,
                    msgType = attachment.msgType,
                    caption = caption,
                    quotedId = quotedId,
                    onProgress = { percent -> reportProgress(clientMsgId, percent) },
                )
            ) {
                is MediaSendOutcome.Success -> reconcileMediaSent(clientMsgId, outcome.id)
                is MediaSendOutcome.Failed -> markMediaFailed(clientMsgId, outcome.reason, outcome.retryable)
            }
        }
    }

    private fun reportProgress(clientMsgId: String, percent: Int) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        val previous = (current.uploads[clientMsgId] as? MediaUploadState.InProgress)?.percent
        _uiState.value = current.copy(
            uploads = current.uploads + (clientMsgId to MediaUploadState.InProgress(clampUploadProgress(previous, percent))),
        )
    }

    private fun reconcileMediaSent(clientMsgId: String, serverId: String) {
        reconcileSent(clientMsgId, serverId)
        val current = _uiState.value as? ConversationUiState.Content ?: return
        _uiState.value = current.copy(uploads = current.uploads - clientMsgId)
    }

    private fun markMediaFailed(clientMsgId: String, reason: String, retryable: Boolean) {
        markFailed(clientMsgId)
        val current = _uiState.value as? ConversationUiState.Content ?: return
        _uiState.value = current.copy(
            uploads = current.uploads + (clientMsgId to MediaUploadState.Failed(reason, retryable)),
        )
    }

    private fun mediaFileFromUrl(url: String): File = File(URI(url))

    private fun load() {
        _uiState.value = ConversationUiState.Loading
        viewModelScope.launch {
            _uiState.value = when (val result = repository.messages(jid)) {
                is MessagesResult.Success -> ConversationUiState.Content(
                    messages = result.messages,
                    backfilling = result.backfilling,
                )
                is MessagesResult.Error -> ConversationUiState.Error(result.reason)
            }
        }
    }

    private fun observeConnectionState() {
        viewModelScope.launch {
            eventSource.state.collect { connectionState ->
                if (connectionState is WhatsAppConnectionState.Live) {
                    if (hasConnectedOnce) {
                        load()
                    }
                    hasConnectedOnce = true
                }
            }
        }
    }

    private fun observeEvents() {
        viewModelScope.launch {
            eventSource.events.collect { event -> applyEvent(event) }
        }
    }

    private fun applyEvent(event: WhatsAppWsEvent) {
        val current = _uiState.value as? ConversationUiState.Content ?: return
        when (event) {
            is WhatsAppWsEvent.MessageReceived -> {
                val incoming = event.message
                if (incoming.chatJid != jid) return
                if (current.messages.any { it.id == incoming.id }) return
                val pendingIndex = if (incoming.fromMe) {
                    current.messages.indexOfFirst {
                        it.clientMsgId != null && it.sendStatus == MessageSendStatus.SENDING && it.text == incoming.text
                    }
                } else {
                    -1
                }
                val messages = if (pendingIndex >= 0) {
                    current.messages.toMutableList().apply { set(pendingIndex, incoming) }
                } else {
                    current.messages + incoming
                }
                _uiState.value = current.copy(messages = messages)
            }
            is WhatsAppWsEvent.Ack -> {
                val messages = current.messages.map { msg ->
                    if (msg.id == event.messageId) msg.copy(ack = event.ackLevel.toLong()) else msg
                }
                _uiState.value = current.copy(messages = messages)
            }
            is WhatsAppWsEvent.Reaction -> {
                if (event.chatJid != null && event.chatJid != jid) return
                val messages = current.messages.map { msg ->
                    if (msg.id != event.messageId) {
                        msg
                    } else {
                        val withoutSameSender = msg.reactions.filterNot { it.from == event.from }
                        val reactions = if (event.emoji.isEmpty()) {
                            withoutSameSender
                        } else {
                            withoutSameSender + WhatsAppReaction(
                                emoji = event.emoji,
                                from = event.from,
                                ts = System.currentTimeMillis() / 1000,
                            )
                        }
                        msg.copy(reactions = reactions)
                    }
                }
                _uiState.value = current.copy(messages = messages)
            }
        }
    }
}
