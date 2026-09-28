package dev.servercontrolpanel.data.terminal

import dev.servercontrolpanel.data.offline.Outbox
import dev.servercontrolpanel.data.offline.IdempotencyProof
import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.api.TerminalApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.AssignSessionRequest
import dev.servercontrolpanel.mobileapiclient.model.CreateBackupRequest
import dev.servercontrolpanel.mobileapiclient.model.KillSessionRequest
import dev.servercontrolpanel.mobileapiclient.model.RenameSessionRequest
import dev.servercontrolpanel.mobileapiclient.model.RestoreBackupRequest
import dev.servercontrolpanel.mobileapiclient.model.WSTicketRequest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

data class TerminalSession(val name: String, val attached: Boolean, val created: Long, val tab: String?)

sealed interface TerminalSessionsResult {
    data class Success(val sessions: List<TerminalSession>) : TerminalSessionsResult
    data object Empty : TerminalSessionsResult
    data class Error(val reason: String) : TerminalSessionsResult
}

interface TerminalSessionsSource {
    suspend fun sessions(): TerminalSessionsResult
}

sealed interface ScrollbackResult {
    data class Success(val text: String) : ScrollbackResult
    data class Error(val reason: String) : ScrollbackResult
}

sealed interface RawLogResult {
    data class Success(val bytes: ByteArray, val total: Int) : RawLogResult
    data class Error(val reason: String) : RawLogResult
}

interface TerminalRawLogSource {
    suspend fun rawLog(name: String, bytes: Int): RawLogResult

    suspend fun history(name: String, bytes: Int): RawLogResult
}

interface TerminalScrollbackSource {
    suspend fun scrollback(name: String, lines: Int = 5000, plain: Boolean = false): ScrollbackResult
}

class TerminalRepository(
    private val mobileApi: MobileApi = MobileApi(),
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
        val response = mobileApi.getTerminalHistory(name = name, bytes = bytes.toLong())
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

private fun bodyJson(vararg fields: Pair<String, String?>): String =
    kotlinx.serialization.json.JsonObject(
        fields.filter { it.second != null }
            .associate { (key, value) -> key to kotlinx.serialization.json.JsonPrimitive(value) },
    ).toString()

private fun urlEncode(v: String): String = java.net.URLEncoder.encode(v, "UTF-8")

const val TARGET_ALL: String = "*"

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
