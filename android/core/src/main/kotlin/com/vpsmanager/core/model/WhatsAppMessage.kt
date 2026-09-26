package com.vpsmanager.core.model

/**
 * How a locally-sent message currently stands, from the sender's own device
 * perspective. [SENT] covers every message this device did not just send
 * (including everything loaded from history or received live) as well as a
 * send this device made that the server already confirmed.
 */
enum class MessageSendStatus {
    SENT,
    SENDING,
    FAILED,

    /**
     * Held in the local queue: it goes out on its own once the internet is back.
     *
     * A state of its OWN and not a longer SENDING. The difference changes what
     * the person does: a "sending" that never ends makes them wait and then try
     * again; a "queued" says it is already settled and they can put the phone
     * away. And an eternal SENDING is indistinguishable from a frozen app,
     * which is the likelier reading after thirty seconds of staring at it.
     */
    NA_FILA,
}

/**
 * A single message in a WhatsApp conversation. Pure domain shape -- the
 * generated OpenAPI client's `MessageView` DTO and the `/ws/whatsapp` wire
 * event never cross the `:data` boundary; `WhatsAppRepository` and the WS
 * event mapper both produce this same shape.
 *
 * [clientMsgId] and [sendStatus] exist only for messages this device is
 * currently sending or just sent -- every message loaded from history or
 * received live from someone else carries `clientMsgId = null` and
 * `sendStatus = MessageSendStatus.SENT`. [id] is always the server-issued
 * identity used for dedup; [clientMsgId] is only ever the client-generated
 * idempotency key sent to `POST .../messages`, reconciled away once the
 * server confirms a real [id] (or an equivalent WS event arrives first).
 */
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
