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

/** Outcome of listing the WhatsApp chats (`GET /api/mobile/v1/whatsapp/chats`). */
sealed interface ChatsResult {
    data class Success(val chats: List<WhatsAppChat>) : ChatsResult
    data object Empty : ChatsResult
    data class Error(val reason: String) : ChatsResult
}

/** Outcome of loading a chat's message history. */
sealed interface MessagesResult {
    data class Success(val messages: List<WhatsAppMessage>, val backfilling: Boolean) : MessagesResult
    data class Error(val reason: String) : MessagesResult
}

/** Outcome of sending a text message. */
sealed interface SendResult {
    data class Success(val id: String) : SendResult
    data class Error(val reason: String) : SendResult

    /**
     * No network — the message was STORED and goes out when the internet is
     * back.
     *
     * It is a third result rather than an `Error` because the difference
     * changes what the person does: faced with an error they try again; faced
     * with a queued message they put the phone away. Treating the two as
     * failure was the old behaviour, and it made the app ask for an action that
     * was not needed.
     */
    data object Queued : SendResult
}

/**
 * Outcome of uploading a media attachment (`POST .../media`).
 * [Error.overCap] is `true` for a server 413 (the real 100MiB enforcement
 * boundary) so callers can show the exact same inline "file too large"
 * state a client-side pre-check would have shown, instead of a generic
 * failure with a misleading retry affordance.
 */
sealed interface UploadResult {
    data class Success(val id: String) : UploadResult
    data class Error(val reason: String, val overCap: Boolean = false) : UploadResult
}

/**
 * The single call site into the generated mobile BFF client
 * (`:data:mobile-api-client`) for WhatsApp operations. No other module may
 * reference [WhatsappApi] or its generated model types directly -- callers
 * only ever see [ChatsResult]/[MessagesResult]/[SendResult] and the pure
 * `dev.servercontrolpanel.core.model.WhatsApp*` domain shapes.
 *
 * Open (class and every member) so `:feature-whatsapp`'s ViewModel tests can
 * substitute a fake at this seam without ever seeing [WhatsappApi] -- that
 * type stays invisible outside `:data` because this module depends on
 * `:data:mobile-api-client` with `implementation`, not `api`.
 */
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
        // HERE THE QUEUE FINALLY COMES IN.
        //
        // This is the only write path in the app that qualifies: the BFF
        // deduplicates a message send by `client_msg_id` in the BODY
        // (`internal/mobilebff/handlers_whatsapp.go`), so resending later is
        // safe even if the first attempt arrived and only the response was
        // lost — which is the situation a timeout produces and leaves
        // indistinguishable.
        //
        // If the queue refuses (not installed, or at its ceiling), it falls
        // through to the usual error: a message refused in silence would be
        // worse than the visible failure that existed before.
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

    /**
     * Uploads a local media file through the multipart media endpoint.
     * [onProgress] is invoked (0-100) as bytes are actually written to the
     * socket -- a fresh [WhatsappApi] is built per call, reusing this
     * instance's own [whatsappApi]`.baseUrl` (so it targets the same server
     * a test's or production's injected instance already does) but wrapping
     * [ApiClient.defaultClient] with one interceptor scoped to this single
     * call's progress callback, mirroring `MediaNetwork.mediaCallFactory`'s
     * "derive, don't replace" pattern for the shared `Call.Factory`.
     *
     * [caption]/[msgType]/[quotedId] are coerced from `null` to `""` before
     * reaching the generated client: its multipart serializer force-casts
     * every non-file form part's value with a non-null `as kotlin.String`
     * regardless of the field's own optionality, so a `null` here throws a
     * `ClassCastException` deep inside `uploadWhatsAppMedia` instead of
     * omitting the part. An empty string sidesteps that crash and is
     * indistinguishable from an absent field on the BFF side (Go's
     * `FormValue` already returns `""` for a field that was never sent, and
     * the endpoint's own contract documents an empty `msg_type` as "infer
     * from mime/name") -- so this is a same-request-shape workaround, not a
     * behavior change, and needs no change to the generated client or its
     * codegen templates.
     */
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

/**
 * Wraps a multipart [RequestBody] so every chunk actually written to the
 * socket reports a 0-100 percentage back through [onProgress] -- the
 * standard OkHttp "counting sink" pattern, since neither the generated
 * client's [WhatsappApi.uploadWhatsAppMedia] nor OkHttp itself expose upload
 * progress natively. [isOneShot] is `true` because a real upload never needs
 * OkHttp to re-serialize this body from scratch (a failed attempt is retried
 * by the caller constructing a brand new request, not by OkHttp replaying
 * this one).
 */
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

/**
 * A [Call.Factory] scoped to exactly one upload attempt -- derived from
 * [ApiClient.defaultClient] via `newBuilder()` (same connection pool,
 * dispatcher and TLS/proxy config every other BFF call already trusts) plus
 * one interceptor wrapping the outgoing body in [ProgressRequestBody]. A
 * fresh instance per call rather than a shared singleton, because the
 * progress callback it closes over belongs to that one send/retry only.
 */
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

/**
 * The send body, assembled by hand for the queue.
 *
 * By hand and not through the generated client because the queue stores TEXT:
 * it needs JSON that survives closing the app and rebooting the device, and the
 * generated client's object is neither serializable nor stable across versions.
 * The shape here is the same one `SendMessageRequest` produces — three fields,
 * all documented in the BFF's OpenAPI.
 *
 * `quoted_id` only goes in when it exists: sending an explicit `null` is not
 * the same as omitting it for a Go decoder with a non-nullable field.
 */
private fun sendBody(clientMsgId: String, text: String, quotedId: String?): String {
    val fields = buildMap {
        put("client_msg_id", clientMsgId)
        put("text", text)
        if (quotedId != null) put("quoted_id", quotedId)
    }
    // A JsonObject assembled by hand rather than a MapSerializer: the String
    // serializer lives in `kotlinx.serialization.builtins.serializer()` as an
    // EXTENSION on KSerializer.Companion, and calling it qualified does not
    // resolve. Building the object is more direct and does not depend on which
    // overload the compiler picks.
    return kotlinx.serialization.json.JsonObject(
        fields.mapValues { (_, value) -> kotlinx.serialization.json.JsonPrimitive(value) },
    ).toString()
}
