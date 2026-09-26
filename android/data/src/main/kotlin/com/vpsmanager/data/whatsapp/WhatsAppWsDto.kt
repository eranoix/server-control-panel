package com.vpsmanager.data.whatsapp

import com.vpsmanager.core.model.WhatsAppMedia
import com.vpsmanager.core.model.WhatsAppMessage
import com.vpsmanager.core.model.WhatsAppReaction
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * Wire shape of `internal/whatsapp/types.go`'s `WSEvent`, as pushed on
 * `/ws/whatsapp`. Deliberately narrower than the Go struct -- fields this
 * plan's screens never consume (`state`, `qr` snapshot fields, `presence`)
 * are left unmodeled; an unknown/unmodeled `kind` decodes fine (all fields
 * optional) and [toDomainEvent] maps it to null.
 */
@Serializable
internal data class WsEventDto(
    @SerialName("kind") val kind: String,
    @SerialName("ts") val ts: Long = 0,
    @SerialName("message") val message: WsMessageDto? = null,
    @SerialName("ack_id") val ackId: String? = null,
    @SerialName("ack_n") val ackN: Int? = null,
    @SerialName("chat_jid") val chatJid: String? = null,
    @SerialName("reaction_from") val reactionFrom: String? = null,
    @SerialName("reaction_emoji") val reactionEmoji: String? = null,
)

/** Wire shape of `internal/whatsapp/types.go`'s `Message`, embedded in a `"message"`-kind event. */
@Serializable
internal data class WsMessageDto(
    @SerialName("id") val id: String,
    @SerialName("chat") val chatJid: String,
    @SerialName("from_me") val fromMe: Boolean = false,
    @SerialName("ts") val ts: Long = 0,
    @SerialName("type") val type: String = "text",
    @SerialName("body") val body: String? = null,
    @SerialName("media") val media: WsMediaDto? = null,
    @SerialName("quoted_id") val quotedId: String? = null,
    @SerialName("ack") val ack: Int = 0,
    @SerialName("reactions") val reactions: List<WsReactionDto>? = null,
)

@Serializable
internal data class WsMediaDto(
    @SerialName("path") val path: String,
    @SerialName("mime") val mime: String? = null,
    @SerialName("size") val size: Long? = null,
    @SerialName("filename") val filename: String? = null,
    @SerialName("duration") val duration: Int? = null,
    @SerialName("w") val width: Int? = null,
    @SerialName("h") val height: Int? = null,
)

@Serializable
internal data class WsReactionDto(
    @SerialName("from") val from: String,
    @SerialName("emoji") val emoji: String,
    @SerialName("ts") val ts: Long = 0,
)

/**
 * A `/ws/whatsapp` event, mapped to the same domain shapes REST history uses
 * -- [WhatsAppWsEvent.MessageReceived] carries a full [WhatsAppMessage] so
 * the caller can merge it into a message list exactly like a REST-loaded one.
 */
sealed interface WhatsAppWsEvent {
    data class MessageReceived(val message: WhatsAppMessage) : WhatsAppWsEvent
    data class Ack(val messageId: String, val ackLevel: Int) : WhatsAppWsEvent
    data class Reaction(val chatJid: String?, val messageId: String, val from: String, val emoji: String) : WhatsAppWsEvent
}

private val wsJson = Json { ignoreUnknownKeys = true }

/**
 * Parses a raw `/ws/whatsapp` text frame into a [WhatsAppWsEvent], or null
 * for a `kind` this plan's screens do not act on (`status`, `qr`, `chat`,
 * `presence`, `revoked`) or a frame this device cannot make sense of
 * (malformed JSON, a `"message"` event missing its `message` payload, an
 * `"ack"`/`"reaction"` event missing its id) -- callers simply drop it.
 */
fun parseWhatsAppWsEvent(text: String): WhatsAppWsEvent? {
    val dto = try {
        wsJson.decodeFromString(WsEventDto.serializer(), text)
    } catch (e: Exception) {
        return null
    }
    return when (dto.kind) {
        "message" -> dto.message?.let { WhatsAppWsEvent.MessageReceived(it.toDomain()) }
        "ack" -> dto.ackId?.let { WhatsAppWsEvent.Ack(messageId = it, ackLevel = dto.ackN ?: 0) }
        "reaction" -> dto.ackId?.let {
            WhatsAppWsEvent.Reaction(
                chatJid = dto.chatJid,
                messageId = it,
                from = dto.reactionFrom.orEmpty(),
                emoji = dto.reactionEmoji.orEmpty(),
            )
        }
        else -> null
    }
}

private fun WsMessageDto.toDomain() = WhatsAppMessage(
    id = id,
    chatJid = chatJid,
    fromMe = fromMe,
    sender = null,
    text = body,
    type = type,
    ts = ts,
    ack = ack.toLong(),
    quotedId = quotedId,
    media = media?.toDomain(),
    reactions = reactions.orEmpty().map { it.toDomain() },
)

private fun WsMediaDto.toDomain() = WhatsAppMedia(
    url = path,
    mimeType = mime,
    filename = filename,
    size = size,
    duration = duration?.toLong(),
    width = width?.toLong(),
    height = height?.toLong(),
)

private fun WsReactionDto.toDomain() = WhatsAppReaction(emoji = emoji, from = from, ts = ts)
