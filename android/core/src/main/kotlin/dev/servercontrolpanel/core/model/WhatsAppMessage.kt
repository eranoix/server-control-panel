package dev.servercontrolpanel.core.model

enum class MessageSendStatus {
    SENT,
    SENDING,
    FAILED,

    QUEUED,
}

data class WhatsAppMessage(
    val id: String,
    val chatJid: String,
    val fromMe: Boolean,
    val sender: String?,
    val text: String?,
    val type: String,
    val ts: Long,
    val ack: Long,
    val quotedId: String?,
    val media: WhatsAppMedia?,
    val reactions: List<WhatsAppReaction>,
    val clientMsgId: String? = null,
    val sendStatus: MessageSendStatus = MessageSendStatus.SENT,
)

data class WhatsAppMedia(
    val url: String,
    val mimeType: String?,
    val filename: String?,
    val size: Long?,
    val duration: Long?,
    val width: Long?,
    val height: Long?,
)

data class WhatsAppReaction(
    val emoji: String,
    val from: String,
    val ts: Long,
)
