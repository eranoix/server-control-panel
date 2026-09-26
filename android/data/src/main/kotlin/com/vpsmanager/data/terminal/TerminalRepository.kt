package com.vpsmanager.data.terminal

import com.vpsmanager.data.offline.Outbox
import com.vpsmanager.data.offline.IdempotencyProof
import com.vpsmanager.mobileapiclient.api.MobileApi
import com.vpsmanager.mobileapiclient.api.TerminalApi
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.model.AssignSessionRequest
import com.vpsmanager.mobileapiclient.model.CreateBackupRequest
import com.vpsmanager.mobileapiclient.model.KillSessionRequest
import com.vpsmanager.mobileapiclient.model.RenameSessionRequest
import com.vpsmanager.mobileapiclient.model.RestoreBackupRequest
import com.vpsmanager.mobileapiclient.model.WSTicketRequest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

/** One session the authenticated user can see (own, or shared via `all` audience). */
data class TerminalSession(val name: String, val attached: Boolean, val created: Long, val tab: String?)

/** Outcome of `GET /api/mobile/v1/terminal/sessions`. */
sealed interface TerminalSessionsResult {
    data class Success(val sessions: List<TerminalSession>) : TerminalSessionsResult
    data object Empty : TerminalSessionsResult
    data class Error(val reason: String) : TerminalSessionsResult
}

/**
 * The slice of [TerminalRepository] that `SessionListViewModel` depends on.
 * `:feature-terminal` cannot see [MobileApi], so its tests fake this interface.
 */
interface TerminalSessionsSource {
    suspend fun sessions(): TerminalSessionsResult
}

/** Outcome of `GET /api/mobile/v1/terminal/scrollback`. */
sealed interface ScrollbackResult {
    data class Success(val text: String) : ScrollbackResult
    data class Error(val reason: String) : ScrollbackResult
}

/**
 * A session's RAW log: the bytes the PTY wrote, escapes included. [total] is the
 * server-side log size; when `total > bytes.size` older history did not fit.
 */
sealed interface RawLogResult {
    data class Success(val bytes: ByteArray, val total: Int) : RawLogResult
    data class Error(val reason: String) : RawLogResult
}

/**
 * Fetches the raw log so the app can prime its own emulator on attach. Separate
 * from [TerminalScrollbackSource], which returns text to be read rather than a
 * stream to be replayed. See `RawLogResponse` on the Go side.
 */
interface TerminalRawLogSource {
    suspend fun rawLog(name: String, bytes: Int): RawLogResult

    /**
     * The rendered history: lines already scrolled off the screen, as
     * append-only text. Preferred for the primer, because replaying the raw log
     * duplicates lines (see `AttachReplay`); the server builds it with a live
     * emulator on the session grid. Measured: 4096 KiB of raw log become 360 KiB
     * of history.
     *
     * Empty is valid (new session, nothing scrolled yet); the primer then falls
     * back to [rawLog].
     */
    suspend fun history(name: String, bytes: Int): RawLogResult
}

/** The "load older" seam that `TerminalViewModel` tests fake. */
interface TerminalScrollbackSource {
    suspend fun scrollback(name: String, lines: Int = 5000, plain: Boolean = false): ScrollbackResult
}

/**
 * The single call site into the generated BFF client for terminal sessions, WS
 * tickets, history and backups. No other module may reference [MobileApi] or its
 * model types; callers only see the sealed results here.
 */
class TerminalRepository(
    private val mobileApi: MobileApi = MobileApi(),
    // Kill, assign and peek live under the BFF's `terminal` tag, so the generator
    // put them in a separate class (same base URL and auth).
    private val terminalApi: TerminalApi = TerminalApi(),
) : TerminalTicketSource,
    TerminalSessionsSource,
    TerminalScrollbackSource,
    TerminalRawLogSource,
    TerminalBackupSource {

    override suspend fun sessions(): TerminalSessionsResult = try {
        val response = mobileApi.listTerminalSessions()
        if (response.isEmpty()) {
            TerminalSessionsResult.Empty
        } else {
            TerminalSessionsResult.Success(
                response.map { TerminalSession(it.name, it.attached, it.created, it.tab) },
            )
        }
    } catch (e: ClientException) {
        TerminalSessionsResult.Error("Could not load the sessions (error ${e.statusCode}).")
    } catch (e: ServerException) {
        TerminalSessionsResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        TerminalSessionsResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        TerminalSessionsResult.Error("Configuration error while loading the sessions.")
    } catch (e: UnsupportedOperationException) {
        TerminalSessionsResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        TerminalSessionsResult.Error("Could not load the sessions.")
    }

    override suspend fun wsTicket(name: String): WsTicketResult = try {
        val response = mobileApi.issueTerminalWSTicket(WSTicketRequest(name = name))
        WsTicketResult.Success(ticket = response.ticket, expiresIn = response.expiresIn.toInt())
    } catch (e: ClientException) {
        WsTicketResult.Error("Could not start the terminal (error ${e.statusCode}).")
    } catch (e: ServerException) {
        WsTicketResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        WsTicketResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        WsTicketResult.Error("Configuration error while starting the terminal.")
    } catch (e: UnsupportedOperationException) {
        WsTicketResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        WsTicketResult.Error("Could not start the terminal.")
    }

    override suspend fun scrollback(name: String, lines: Int, plain: Boolean): ScrollbackResult = try {
        val response = mobileApi.getTerminalScrollback(name = name, lines = lines.toLong(), plain = plain)
        ScrollbackResult.Success(response.data)
    } catch (e: ClientException) {
        ScrollbackResult.Error("Could not load the history (error ${e.statusCode}).")
    } catch (e: ServerException) {
        ScrollbackResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        ScrollbackResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        ScrollbackResult.Error("Configuration error while loading the history.")
    } catch (e: UnsupportedOperationException) {
        ScrollbackResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        ScrollbackResult.Error("Could not load the history.")
    }

    override suspend fun history(name: String, bytes: Int): RawLogResult = try {
        val response = mobileApi.getTerminalHistorico(name = name, bytes = bytes.toLong())
        // Decode off the caller's dispatcher: a few MiB of base64 on the main
        // thread is a visible freeze.
        withContext(Dispatchers.Default) {
            RawLogResult.Success(
                bytes = java.util.Base64.getDecoder().decode(response.base64),
                total = response.total.toInt(),
            )
        }
    } catch (e: ClientException) {
        RawLogResult.Error("Could not load the history (error ${e.statusCode}).")
    } catch (e: ServerException) {
        RawLogResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        RawLogResult.Error("Connection failed while loading the history.")
    } catch (e: IllegalArgumentException) {
        RawLogResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        RawLogResult.Error("Could not load the history.")
    }

    override suspend fun rawLog(name: String, bytes: Int): RawLogResult = try {
        val response = mobileApi.getTerminalRawLog(name = name, bytes = bytes.toLong())
        // The generated client returns to the caller's dispatcher before
        // decoding, and up to ~22 MiB of base64 would freeze the main thread.
        withContext(Dispatchers.Default) {
            RawLogResult.Success(
                // java.util's Base64 works on the device (minSdk 34) and on the
                // JVM, so unit tests run the same path.
                bytes = java.util.Base64.getDecoder().decode(response.base64),
                total = response.total.toInt(),
            )
        }
    } catch (e: ClientException) {
        RawLogResult.Error("Could not load the history (error ${e.statusCode}).")
    } catch (e: ServerException) {
        RawLogResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        RawLogResult.Error("Connection failed while loading the history.")
    } catch (e: IllegalArgumentException) {
        // Corrupt base64: better to open without history than crash the screen.
        RawLogResult.Error("The history arrived corrupted from the server.")
    } catch (e: IllegalStateException) {
        RawLogResult.Error("Configuration error while loading the history.")
    } catch (e: Exception) {
        RawLogResult.Error("Could not load the history.")
    }

    override suspend fun backups(): BackupsResult = try {
        val response = mobileApi.listTerminalBackups()
        if (response.isEmpty()) {
            BackupsResult.Empty
        } else {
            BackupsResult.Success(
                response.map { bk ->
                    SessionBackup(
                        id = bk.id,
                        createdAt = bk.created,
                        origin = bk.source,
                        bytes = bk.bytes,
                        sessions = bk.sessions.orEmpty().map {
                            BackupSession(name = it.name, summary = it.summary, lines = it.lines.toInt())
                        },
                    )
                },
            )
        }
    } catch (e: Exception) {
        BackupsResult.Error(reasonOf(e, "load the backups"))
    }

    override suspend fun createBackup(session: String?): ActionResult = try {
        val r = mobileApi.createTerminalBackup(CreateBackupRequest(name = session))
        val n = r.count.toInt()
        ActionResult.Ok(if (n == 1) "1 session saved." else "$n sessions saved.")
    } catch (e: IOException) {
        enqueue(
            method = "POST",
            path = "/terminal/backups",
            bodyJson = bodyJson("name" to session),
            description = if (session == null) "backup of all sessions" else "backup of $session",
            action = "save the backup",
        )
    } catch (e: Exception) {
        ActionResult.Error(reasonOf(e, "save the backup"))
    }

    override suspend fun restore(id: String, session: String?): ActionResult = try {
        val r = mobileApi.restoreTerminalBackup(RestoreBackupRequest(id = id, name = session))
        val done = r.restored.toInt()
        val skipped = r.skipped.toInt()
        // "Skipped" almost always means the name is already live (restore never
        // overwrites a session in use), so say it explicitly.
        val text = when {
            done == 0 && skipped > 0 -> "Nothing restored — $skipped already existed or were over the limit."
            skipped > 0 -> "$done restored; $skipped skipped because they already exist."
            done == 1 -> "1 session restored."
            else -> "$done sessions restored."
        }
        ActionResult.Ok(text)
    } catch (e: IOException) {
        enqueue(
            method = "POST",
            path = "/terminal/backups/restore",
            bodyJson = bodyJson("id" to id, "name" to session),
            description = if (session == null) "backup restore" else "restore of $session",
            action = "restore the backup",
        )
    } catch (e: Exception) {
        ActionResult.Error(reasonOf(e, "restore the backup"))
    }

    override suspend fun deleteBackup(id: String, session: String?): ActionResult = try {
        mobileApi.deleteTerminalBackup(id = id, name = session)
        ActionResult.Ok(if (session == null) "Backup deleted." else "Session removed from the backup.")
    } catch (e: IOException) {
        enqueue(
            method = "DELETE",
            // No body: the id goes in the path and the session in the query, as the route expects.
            path = "/terminal/backups/" + urlEncode(id) +
                (session?.let { "?name=" + urlEncode(it) } ?: ""),
            bodyJson = "",
            description = if (session == null) "backup deletion" else "removal of $session from the backup",
            action = "delete the backup",
        )
    } catch (e: Exception) {
        ActionResult.Error(reasonOf(e, "delete the backup"))
    }

    override suspend fun renameSession(from: String, to: String): ActionResult = try {
        mobileApi.renameTerminalSession(RenameSessionRequest(from = from, to = to))
        ActionResult.Ok("Renamed to $to.")
    } catch (e: IOException) {
        enqueue(
            method = "POST",
            path = "/terminal/sessions/rename",
            bodyJson = bodyJson("from" to from, "to" to to),
            description = "rename $from to $to",
            action = "rename the session",
        )
    } catch (e: Exception) {
        ActionResult.Error(reasonOf(e, "rename the session"))
    }

    override suspend fun killSession(name: String): ActionResult = try {
        terminalApi.killTerminalSession(KillSessionRequest(name = name))
        ActionResult.Ok("Session $name ended.")
    } catch (e: Exception) {
        ActionResult.Error(reasonOf(e, "end the session"))
    }

    override suspend fun assignSession(name: String, target: String): ActionResult = try {
        terminalApi.assignTerminalSession(AssignSessionRequest(name = name, target = target))
        // State the result ("everyone can now see it"), not the action.
        val who = if (target == TARGET_ALL) "everyone" else target
        ActionResult.Ok("$name is now visible to $who.")
    } catch (e: IOException) {
        enqueue(
            method = "POST",
            path = "/terminal/sessions/assign",
            bodyJson = bodyJson("name" to name, "target" to target),
            description = "change who sees $name",
            action = "change who sees the session",
        )
    } catch (e: Exception) {
        ActionResult.Error(reasonOf(e, "change who sees the session"))
    }

    override suspend fun assignmentTargets(): TargetsResult = try {
        // The generated list is nullable; absent and empty both mean "no targets",
        // and the screen disables the option.
        TargetsResult.Success(terminalApi.listAssignTargets().targets.orEmpty())
    } catch (e: Exception) {
        TargetsResult.Error(reasonOf(e, "list who the session can be shown to"))
    }

    override suspend fun sessionPreview(name: String, lines: Int): PreviewResult = try {
        val r = terminalApi.previewTerminalSession(name = name, lines = lines.toLong())
        PreviewResult.Success(text = r.text, lines = r.lines.toInt())
    } catch (e: Exception) {
        PreviewResult.Error(reasonOf(e, "preview the session"))
    }
}

/**
 * Queues the action for when the network comes back, or returns the usual error
 * if the queue refuses it. Only called from `catch (IOException)`: 4xx and 5xx
 * mean the server heard and stay immediate errors.
 *
 * Uses [IdempotencyProof.KEY_IN_HEADER]: the five routes used here declare
 * `Idempotency-Key` on the BFF.
 */
private fun enqueue(
    method: String,
    path: String,
    bodyJson: String,
    description: String,
    action: String,
): ActionResult {
    val stored = Outbox.enqueue(
        method = method,
        path = path,
        bodyJson = bodyJson,
        description = description,
        proof = IdempotencyProof.KEY_IN_HEADER,
    )
    return if (stored) {
        ActionResult.Queued("No internet — $description is queued and goes out when the network is back.")
    } else {
        ActionResult.Error(reasonOf(IOException(), action))
    }
}

/**
 * Builds the body by hand because the queue stores text that must survive app
 * restarts, and generated model objects are not stable across regenerations.
 * Null fields are omitted, since Go treats an absent field differently from `null`.
 */
private fun bodyJson(vararg fields: Pair<String, String?>): String =
    kotlinx.serialization.json.JsonObject(
        fields.filter { it.second != null }
            .associate { (key, value) -> key to kotlinx.serialization.json.JsonPrimitive(value) },
    ).toString()

private fun urlEncode(v: String): String = java.net.URLEncoder.encode(v, "UTF-8")

/** The target meaning "everyone" in the server's assignment contract. */
const val TARGET_ALL: String = "*"

/**
 * Maps an exception to a user-facing sentence. Each type gets a distinct message
 * because "no network" and "server down" call for opposite actions, and
 * [action] names what failed since the message may appear far from its button.
 */
private fun reasonOf(e: Exception, action: String): String = when (e) {
    is ClientException -> when (e.statusCode) {
        404 -> "Not found — it may have been removed from another device."
        400 -> "The server rejected the request."
        else -> "Could not $action (error ${e.statusCode})."
    }
    is ServerException -> "The server is unavailable right now."
    is IOException -> "Connection failed. Check your network and try again."
    else -> "Could not $action."
}
