package dev.servercontrolpanel.data.whatsapp

import dev.servercontrolpanel.core.model.WhatsAppChat
import dev.servercontrolpanel.core.model.WhatsAppMedia
import dev.servercontrolpanel.core.model.WhatsAppMessage
import dev.servercontrolpanel.core.model.WhatsAppReaction
import dev.servercontrolpanel.mobileapiclient.api.WhatsappApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import java.io.File
import dev.servercontrolpanel.data.offline.Outbox
import dev.servercontrolpanel.data.offline.IdempotencyProof
import java.io.IOException
import okhttp3.Call
import okhttp3.MediaType
import okhttp3.RequestBody
import okio.Buffer
import okio.BufferedSink
import okio.ForwardingSink
import okio.buffer
import dev.servercontrolpanel.mobileapiclient.model.ChatSummary as GeneratedChatSummary
import dev.servercontrolpanel.mobileapiclient.model.MediaView as GeneratedMediaView
import dev.servercontrolpanel.mobileapiclient.model.MessageView as GeneratedMessageView
import dev.servercontrolpanel.mobileapiclient.model.Reaction as GeneratedReaction
import dev.servercontrolpanel.mobileapiclient.model.SendMessageRequest

sealed interface ChatsResult {
    data class Success(val chats: List<WhatsAppChat>) : ChatsResult
    data object Empty : ChatsResult
    data class Error(val reason: String) : ChatsResult
}

sealed interface MessagesResult {
    data class Success(val messages: List<WhatsAppMessage>, val backfilling: Boolean) : MessagesResult
    data class Error(val reason: String) : MessagesResult
}

sealed interface SendResult {
    data class Success(val id: String) : SendResult
    data class Error(val reason: String) : SendResult

    data object Queued : SendResult
}

sealed interface UploadResult {
    data class Success(val id: String) : UploadResult
    data class Error(val reason: String, val overCap: Boolean = false) : UploadResult
}

open class WhatsAppRepository(
    private val whatsappApi: WhatsappApi = WhatsappApi(),
) {
    open suspend fun chats(): ChatsResult = try {
        val chats = whatsappApi.listWhatsAppChats().map { it.toDomain() }
        if (chats.isEmpty()) ChatsResult.Empty else ChatsResult.Success(chats)
    } catch (e: ClientException) {
        ChatsResult.Error("Could not load the chats (error ${e.statusCode}).")
    } catch (e: ServerException) {
        ChatsResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        ChatsResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        ChatsResult.Error("Configuration error while loading the chats.")
    } catch (e: UnsupportedOperationException) {
        ChatsResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        ChatsResult.Error("Could not load the chats.")
    }

    open suspend fun messages(jid: String, before: Long? = null, limit: Long? = null): MessagesResult = try {
        val response = whatsappApi.getWhatsAppMessages(jid = jid, before = before, limit = limit)
        val messages = response.messages.orEmpty().map { it.toDomain(jid) }
        MessagesResult.Success(messages = messages, backfilling = response.backfilling)
    } catch (e: ClientException) {
        MessagesResult.Error("Could not load the messages (error ${e.statusCode}).")
    } catch (e: ServerException) {
        MessagesResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        MessagesResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        MessagesResult.Error("Configuration error while loading the messages.")
    } catch (e: UnsupportedOperationException) {
        MessagesResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        MessagesResult.Error("Could not load the messages.")
    }

    open suspend fun sendMessage(jid: String, clientMsgId: String, text: String, quotedId: String? = null): SendResult = try {
        val response = whatsappApi.sendWhatsAppMessage(
            jid = jid,
            sendMessageRequest = SendMessageRequest(clientMsgId = clientMsgId, text = text, quotedId = quotedId),
        )
        SendResult.Success(response.id)
    } catch (e: ClientException) {
        SendResult.Error("Could not send the message (error ${e.statusCode}).")
    } catch (e: ServerException) {
        SendResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        val stored = Outbox.enqueue(
            method = "POST",
            path = "/whatsapp/chats/$jid/messages",
            bodyJson = sendBody(clientMsgId, text, quotedId),
            description = "message to $jid",
            proof = IdempotencyProof.IN_BODY,
        )
        if (stored) {
            SendResult.Queued
        } else {
            SendResult.Error("Connection failed. Check your network and try again.")
        }
    } catch (e: IllegalStateException) {
        SendResult.Error("Configuration error while sending the message.")
    } catch (e: UnsupportedOperationException) {
        SendResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        SendResult.Error("Could not send the message.")
    }

    open suspend fun uploadMedia(
        jid: String,
        clientMsgId: String,
        file: File,
        caption: String? = null,
        msgType: String? = null,
        quotedId: String? = null,
        onProgress: (percent: Int) -> Unit = {},
    ): UploadResult = try {
        val uploadApi = WhatsappApi(basePath = whatsappApi.baseUrl, client = progressCallFactory(onProgress))
        val response = uploadApi.uploadWhatsAppMedia(
            jid = jid,
            clientMsgId = clientMsgId,
            file = file,
            caption = caption.orEmpty(),
            msgType = msgType.orEmpty(),
            quotedId = quotedId.orEmpty(),
        )
        UploadResult.Success(response.id)
    } catch (e: ClientException) {
        UploadResult.Error(
            reason = "Could not upload the file (error ${e.statusCode}).",
            overCap = e.statusCode == 413,
        )
    } catch (e: ServerException) {
        UploadResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        UploadResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        UploadResult.Error("Configuration error while uploading the file.")
    } catch (e: UnsupportedOperationException) {
        UploadResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        UploadResult.Error("Could not upload the file.")
    }
}

private class ProgressRequestBody(
    private val delegate: RequestBody,
    private val onProgress: (percent: Int) -> Unit,
) : RequestBody() {
    override fun contentType(): MediaType? = delegate.contentType()
    override fun contentLength(): Long = delegate.contentLength()
    override fun isOneShot(): Boolean = true

    override fun writeTo(sink: BufferedSink) {
        val total = contentLength()
        var written = 0L
        val countingSink = object : ForwardingSink(sink) {
            override fun write(source: Buffer, byteCount: Long) {
                super.write(source, byteCount)
                written += byteCount
                if (total > 0) {
                    onProgress(((written * 100) / total).toInt().coerceIn(0, 100))
                }
            }
        }
        val bufferedCountingSink = countingSink.buffer()
        delegate.writeTo(bufferedCountingSink)
        bufferedCountingSink.flush()
    }
}

private fun progressCallFactory(onProgress: (percent: Int) -> Unit): Call.Factory =
    ApiClient.defaultClient.newBuilder()
        .addInterceptor { chain ->
            val original = chain.request()
            val body = original.body
            if (body == null) {
                chain.proceed(original)
            } else {
                chain.proceed(
                    original.newBuilder()
                        .method(original.method, ProgressRequestBody(body, onProgress))
                        .build(),
                )
            }
        }
        .build()

private fun GeneratedChatSummary.toDomain() = WhatsAppChat(
    jid = jid,
    name = name,
    isGroup = isGroup,
    unread = unread,
    avatarUrl = avatarUrl,
    lastMessageAt = lastMessageAt,
    lastMessagePreview = lastMessagePreview,
)

internal fun GeneratedMessageView.toDomain(fallbackChatJid: String) = WhatsAppMessage(
    id = id,
    chatJid = chatJid.ifEmpty { fallbackChatJid },
    fromMe = fromMe,
    sender = sender,
    text = text,
    type = type,
    ts = ts,
    ack = ack,
    quotedId = quotedId,
    media = media?.toDomain(),
    reactions = reactions.orEmpty().map { it.toDomain() },
)

private fun GeneratedMediaView.toDomain() = WhatsAppMedia(
    url = url,
    mimeType = mimeType,
    filename = filename,
    size = propertySize,
    duration = duration,
    width = width,
    height = height,
)

private fun GeneratedReaction.toDomain() = WhatsAppReaction(emoji = emoji, from = from, ts = ts)

private fun sendBody(clientMsgId: String, text: String, quotedId: String?): String {
    val fields = buildMap {
        put("client_msg_id", clientMsgId)
        put("text", text)
        if (quotedId != null) put("quoted_id", quotedId)
    }
    return kotlinx.serialization.json.JsonObject(
        fields.mapValues { (_, value) -> kotlinx.serialization.json.JsonPrimitive(value) },
    ).toString()
}
