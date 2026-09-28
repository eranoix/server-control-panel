package dev.servercontrolpanel.data.videocall

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.io.IOException

private const val WS_VIDEOCALL_PATH = "/ws/videocall"

sealed interface VideocallWsTicketResult {
    data class Success(val ticket: String) : VideocallWsTicketResult
    data class Error(val reason: String) : VideocallWsTicketResult
}

interface VideocallTicketSource {
    suspend fun wsTicket(): VideocallWsTicketResult
}

data class VideocallRoom(val id: String, val name: String, val memberCount: Int)

sealed interface VideocallRoomsResult {
    data class Success(val rooms: List<VideocallRoom>) : VideocallRoomsResult
    data object Empty : VideocallRoomsResult
    data class Error(val reason: String) : VideocallRoomsResult
}

interface VideocallRoomsSource {
    suspend fun rooms(): VideocallRoomsResult
}

class VideocallSignalingRepository(
    private val mobileApi: MobileApi = MobileApi(),
) : VideocallTicketSource, VideocallRoomsSource {

    override suspend fun wsTicket(): VideocallWsTicketResult = try {
        val response = mobileApi.issueVideocallWSTicket()
        VideocallWsTicketResult.Success(ticket = response.ticket)
    } catch (e: ClientException) {
        VideocallWsTicketResult.Error("Could not start the video call (error ${e.statusCode}).")
    } catch (e: ServerException) {
        VideocallWsTicketResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        VideocallWsTicketResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        VideocallWsTicketResult.Error("Configuration error while starting the video call.")
    } catch (e: UnsupportedOperationException) {
        VideocallWsTicketResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        VideocallWsTicketResult.Error("Could not start the video call.")
    }

    override suspend fun rooms(): VideocallRoomsResult = try {
        val response = mobileApi.listVideocallRooms().map {
            VideocallRoom(id = it.id, name = it.name, memberCount = it.members?.size ?: 0)
        }
        if (response.isEmpty()) VideocallRoomsResult.Empty else VideocallRoomsResult.Success(response)
    } catch (e: ClientException) {
        VideocallRoomsResult.Error("Could not load the rooms (error ${e.statusCode}).")
    } catch (e: ServerException) {
        VideocallRoomsResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        VideocallRoomsResult.Error("Connection failed. Check your network and try again.")
    } catch (e: Exception) {
        VideocallRoomsResult.Error("Could not load the rooms.")
    }
}

fun resolveVideocallWsBaseUrl(basePath: String): String? {
    val httpUrl = basePath.toHttpUrlOrNull() ?: return null
    val hostOnly = httpUrl.newBuilder().encodedPath("/").encodedQuery(null).build().toString().removeSuffix("/")
    val wsScheme = if (httpUrl.isHttps) "wss" else "ws"
    return hostOnly.replaceFirst(httpUrl.scheme, wsScheme)
}

fun resolveVideocallWsBaseUrl(): String? = resolveVideocallWsBaseUrl(MobileApi.defaultBasePath)

internal fun buildVideocallConnectUrl(
    wsBaseUrl: String,
    roomId: String,
    clientId: String,
    resume: Boolean,
    ticket: String,
): String {
    val httpBase = wsBaseUrl
        .replaceFirst(Regex("^wss"), "https")
        .replaceFirst(Regex("^ws"), "http")
    val httpUrl = requireNotNull(httpBase.toHttpUrlOrNull()) { "Malformed video call base URL: $wsBaseUrl" }
    val built = httpUrl.newBuilder()
        .encodedPath(WS_VIDEOCALL_PATH)
        .addQueryParameter("room_id", roomId)
        .addQueryParameter("client_id", clientId)
        .addQueryParameter("resume", if (resume) "1" else "0")
        .addQueryParameter("ticket", ticket)
        .build()
    val wsScheme = if (httpUrl.isHttps) "wss" else "ws"
    return built.toString().replaceFirst(httpUrl.scheme, wsScheme)
}

internal interface VideocallWebSocket {
    fun sendText(text: String): Boolean
    fun close(code: Int, reason: String): Boolean
}

internal interface VideocallWebSocketListener {
    fun onOpen()
    fun onTextMessage(text: String)
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

internal fun interface VideocallWebSocketFactory {
    fun open(url: String, listener: VideocallWebSocketListener): VideocallWebSocket
}

internal class OkHttpVideocallWebSocketFactory(
    private val client: OkHttpClient = OkHttpClient(),
) : VideocallWebSocketFactory {

    override fun open(url: String, listener: VideocallWebSocketListener): VideocallWebSocket {
        val request = Request.Builder().url(url).build()
        val socket = client.newWebSocket(
            request,
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    listener.onOpen()
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    listener.onTextMessage(text)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    listener.onClosed(code, reason)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    listener.onFailure(t.message ?: t::class.simpleName ?: "unknown failure")
                }
            },
        )
        return object : VideocallWebSocket {
            override fun sendText(text: String): Boolean = socket.send(text)
            override fun close(code: Int, reason: String): Boolean = socket.close(code, reason)
        }
    }
}

interface VideocallSignaling {
    fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage>
    fun send(msg: SignalingMessage): Boolean
    fun close()
}

class VideocallSignalingClient internal constructor(
    private val ticketSource: VideocallTicketSource,
    private val wsBaseUrl: String,
    private val webSocketFactory: VideocallWebSocketFactory,
) : VideocallSignaling {
    constructor(ticketSource: VideocallTicketSource, wsBaseUrl: String) :
        this(ticketSource, wsBaseUrl, OkHttpVideocallWebSocketFactory())

    private val json = Json { ignoreUnknownKeys = true }

    @Volatile
    private var socket: VideocallWebSocket? = null

    override fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage> = callbackFlow {
        fun dispatch(text: String) {
            try {
                trySend(json.decodeFromString(SignalingMessage.serializer(), text))
            } catch (_: SerializationException) {
            }
        }

        when (val result = ticketSource.wsTicket()) {
            is VideocallWsTicketResult.Error -> {
                trySend(SignalingMessage(type = "error", error = result.reason))
                close()
            }
            is VideocallWsTicketResult.Success -> {
                val url = buildVideocallConnectUrl(wsBaseUrl, roomId, clientId, resume, result.ticket)
                val listener = object : VideocallWebSocketListener {
                    override fun onOpen() = Unit

                    override fun onTextMessage(text: String) = dispatch(text)

                    override fun onClosed(code: Int, reason: String) {
                        socket = null
                        close()
                    }

                    override fun onFailure(reason: String) {
                        socket = null
                        trySend(SignalingMessage(type = "error", error = reason))
                        close()
                    }
                }
                socket = webSocketFactory.open(url, listener)
            }
        }

        awaitClose {
            socket?.close(1000, "leave")
            socket = null
        }
    }

    override fun send(msg: SignalingMessage): Boolean {
        val current = socket ?: return false
        return current.sendText(json.encodeToString(SignalingMessage.serializer(), msg))
    }

    override fun close() {
        val current = socket ?: return
        current.sendText(json.encodeToString(SignalingMessage.serializer(), SignalingMessage(type = "leave")))
        current.close(1000, "leave")
        socket = null
    }
}
