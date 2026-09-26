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

/** Outcome of `POST /api/mobile/v1/videocall/ws-ticket`. Never `Empty` — a successful call always returns a usable one-shot ticket. */
sealed interface VideocallWsTicketResult {
    data class Success(val ticket: String) : VideocallWsTicketResult
    data class Error(val reason: String) : VideocallWsTicketResult
}

/**
 * The narrow slice [VideocallSignalingClient] depends on: a fresh, never-reused ticket for
 * every `connect()` call. Kept as its own interface (rather than a direct dependency on
 * [VideocallSignalingRepository]) purely so tests can supply a fake without touching the
 * generated client or the network.
 */
interface VideocallTicketSource {
    suspend fun wsTicket(): VideocallWsTicketResult
}

/** One room the authenticated user can join for a videocall. */
data class VideocallRoom(val id: String, val name: String, val memberCount: Int)

/** Outcome of `GET /api/mobile/v1/videocall/rooms`. */
sealed interface VideocallRoomsResult {
    data class Success(val rooms: List<VideocallRoom>) : VideocallRoomsResult
    data object Empty : VideocallRoomsResult
    data class Error(val reason: String) : VideocallRoomsResult
}

/**
 * The narrow slice `:feature-videocall`'s `RoomLobbyViewModel` depends on — mirrors
 * [VideocallTicketSource]'s shape/reason: `:feature-videocall` has no compile-time visibility of
 * [MobileApi] or its generated model types, so its tests fake this interface directly.
 */
interface VideocallRoomsSource {
    suspend fun rooms(): VideocallRoomsResult
}

/**
 * The single call site into the generated mobile BFF client (`:data:mobile-api-client`) for
 * `/videocall/ws-ticket` and `/videocall/rooms` — mirrors
 * `dev.servercontrolpanel.data.events.MobileEventsRepository`'s exact shape. The module boundary forbids
 * the app from calling the web panel's cookie-authenticated `GET /api/auth/ws-ticket` directly
 * (that route is not one of the `/api/mobile/v1` BFF endpoints); this class instead calls
 * `POST /api/mobile/v1/videocall/ws-ticket`, a thin per-feature wrapper added to
 * `internal/mobilebff/handlers_videocall.go`, mirroring the pre-existing `/terminal/ws-ticket`
 * and `/events/ws-ticket` endpoints exactly (same one-shot 60s ticket mechanism, reused a third
 * time rather than duplicated). No other module may reference [MobileApi] or its generated model
 * types directly; callers only ever see [VideocallWsTicketResult]/[VideocallRoomsResult] here.
 */
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

/**
 * Derives the `/ws/videocall` origin (scheme + host:port, no path) from the same seam every
 * generated BFF call already reads (`ApiClient.BASE_URL_KEY`/`MobileApi.defaultBasePath`) —
 * mirrors `dev.servercontrolpanel.data.terminal.defaultTerminalWsBaseUrl`'s exact reasoning. Returns
 * `null` when [basePath] cannot be parsed as an `http(s)` URL (e.g. the app has not been
 * configured with a server address yet); callers must not attempt to connect in that case.
 */
fun resolveVideocallWsBaseUrl(basePath: String): String? {
    val httpUrl = basePath.toHttpUrlOrNull() ?: return null
    val hostOnly = httpUrl.newBuilder().encodedPath("/").encodedQuery(null).build().toString().removeSuffix("/")
    val wsScheme = if (httpUrl.isHttps) "wss" else "ws"
    return hostOnly.replaceFirst(httpUrl.scheme, wsScheme)
}

/** Convenience overload reading [MobileApi.defaultBasePath] directly. */
fun resolveVideocallWsBaseUrl(): String? = resolveVideocallWsBaseUrl(MobileApi.defaultBasePath)

/**
 * Builds the full `/ws/videocall` connect URL, percent-encoding [roomId]/[clientId]/[ticket]
 * through `okhttp3.HttpUrl`'s query-parameter encoder rather than raw string interpolation.
 * `resume` is sent as the literal string `"1"`/`"0"` (never `"true"`/`"false"`) — matching
 * `internal/videocall/ws.go`'s `r.URL.Query().Get("resume") == "1"` check exactly.
 * `HttpUrl` cannot represent a `ws`/`wss` scheme directly, so this builds against a temporary
 * `http(s)` view of [wsBaseUrl] and swaps the scheme back afterwards — the same technique
 * `dev.servercontrolpanel.data.whatsapp.resolveWhatsAppWsUrl` uses.
 */
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

/**
 * Mirrors `dev.servercontrolpanel.data.events.MobileEventsWebSocket`'s seam: abstracts away the concrete
 * OkHttp WebSocket type so [VideocallSignalingClient] can be unit-tested on the JVM with a fake,
 * never a real network call.
 */
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

/**
 * Opens a [VideocallWebSocket] against [url], delivering events to [listener].
 * [OkHttpVideocallWebSocketFactory] is the only production implementation and the only place in
 * this file that constructs a real OkHttp WebSocket.
 */
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

/**
 * The narrow seam `CallViewModel` (`:feature-videocall`) depends on instead of the concrete
 * [VideocallSignalingClient] — the real implementation opens an actual OkHttp WebSocket
 * (`internal`, module-private transport seam), so it cannot be constructed with a fake transport
 * from outside `:data`. `CallViewModelTest` injects a hand-rolled fake implementing this
 * interface directly; [VideocallSignalingClient] is the only production implementation.
 */
interface VideocallSignaling {
    fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage>
    fun send(msg: SignalingMessage): Boolean
    fun close()
}

/**
 * A single `/ws/videocall` connection: the two-step ws-ticket-then-connect flow, decoded inbound
 * [SignalingMessage] frames, and outbound `send`/`close`. Unlike
 * `dev.servercontrolpanel.data.events.MobileEventsSocket`, this class does not own a background
 * reconnect loop — one call, one room, one connection; reconnect-after-drop (the resume
 * flow) is the caller's decision, made explicit by passing `resume = true` and a
 * stable `clientId` to a fresh [connect] call.
 */
class VideocallSignalingClient internal constructor(
    private val ticketSource: VideocallTicketSource,
    private val wsBaseUrl: String,
    private val webSocketFactory: VideocallWebSocketFactory,
) : VideocallSignaling {
    /** Public constructor: always wires the real OkHttp-backed transport (internal type, hidden from callers). */
    constructor(ticketSource: VideocallTicketSource, wsBaseUrl: String) :
        this(ticketSource, wsBaseUrl, OkHttpVideocallWebSocketFactory())

    private val json = Json { ignoreUnknownKeys = true }

    @Volatile
    private var socket: VideocallWebSocket? = null

    /**
     * Fetches exactly one fresh ws-ticket via [ticketSource], then opens `/ws/videocall` with it
     * — the two-step flow the module boundary requires. Every decoded inbound [SignalingMessage]
     * is emitted on the returned [Flow]; a ticket-fetch failure or WS failure is surfaced as a
     * single synthetic `SignalingMessage(type = "error", error = reason)` frame before the flow
     * closes, so callers observe every outcome through one channel instead of a separate
     * exception-handling path. Cancelling collection (or calling [close]) closes the socket.
     */
    override fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage> = callbackFlow {
        fun dispatch(text: String) {
            try {
                trySend(json.decodeFromString(SignalingMessage.serializer(), text))
            } catch (_: SerializationException) {
                // Malformed/unrecognized frame: dropped, not fatal.
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

    /** Encodes and sends [msg] as a text frame. Returns `false` if not currently connected. */
    override fun send(msg: SignalingMessage): Boolean {
        val current = socket ?: return false
        return current.sendText(json.encodeToString(SignalingMessage.serializer(), msg))
    }

    /**
     * Closes the socket cleanly: sends a `leave` message first if still open — matching the
     * existing web client's graceful-leave behavior (`this.ws.send(JSON.stringify({ type:
     * 'leave' }))` in `internal/webassets/web/vendor/panel/videocall.js`), distinct from an
     * ungraceful disconnect the server also has to handle (`onPeerGone`'s two paths in
     * `internal/videocall/signaling.go`) — then closes the WS with code 1000.
     */
    override fun close() {
        val current = socket ?: return
        current.sendText(json.encodeToString(SignalingMessage.serializer(), SignalingMessage(type = "leave")))
        current.close(1000, "leave")
        socket = null
    }
}
