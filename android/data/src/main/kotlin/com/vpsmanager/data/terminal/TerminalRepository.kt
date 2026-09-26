package com.vpsmanager.data.terminal

import com.vpsmanager.data.offline.FilaDeEnvio
import com.vpsmanager.data.offline.ProvaDeIdempotencia
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
 * The narrow slice of [TerminalRepository] that `:feature-terminal`'s
 * `SessionListViewModel` depends on — mirrors [TerminalTicketSource]'s shape
 * exactly, and for the same reason: `:feature-terminal` has no compile-time
 * visibility of [MobileApi], so its tests fake this interface
 * directly instead of the generated client.
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
 * A session's RAW log: the bytes the PTY wrote, escapes and all.
 *
 * [total] is the size of the whole log on the server; [bytes] is what came back
 * in this response. When `total > bytes.size`, there is older history that did
 * not fit the request — the app needs to know that so it does not claim this is
 * all there ever was.
 */
sealed interface RawLogResult {
    data class Success(val bytes: ByteArray, val total: Int) : RawLogResult
    data class Error(val reason: String) : RawLogResult
}

/**
 * Fetches the raw log so the app can prime its OWN emulator on attach.
 *
 * Kept separate from [TerminalScrollbackSource] on purpose: that one returns
 * text to be READ (copied, summarised), this one returns a terminal stream to
 * be REPLAYED. The difference is not one of format but of destination — and
 * conflating the two was exactly the defect this fixes. See `RawLogResponse` on
 * the Go side.
 */
interface TerminalRawLogSource {
    suspend fun logBruto(name: String, bytes: Int): RawLogResult

    /**
     * The RENDERED history: the lines that have already scrolled off the
     * screen, as append-only text.
     *
     * It is the PREFERRED source for the primer, and the reason is in
     * `ReplayDeAttach`'s KDoc: replaying the raw log duplicates, because the
     * `ESC[nA` of a repainting program saturates at the top of the SCREEN and
     * never reaches the scrollback. That text concludes there is no fix — and
     * there is none on the READING side. The server started fixing it on the
     * WRITING side, keeping a live emulator on the session's grid and pouring
     * the departing lines into it. Measured: 4096 KiB of raw log become 360 KiB
     * of history, 11.4x more conversation for the same network budget.
     *
     * Empty is a legitimate answer (a new session, or one that has not scrolled
     * a single line off yet) — the primer then falls back to [logBruto].
     */
    suspend fun historico(name: String, bytes: Int): RawLogResult
}

/** Mirrors [TerminalSessionsSource]'s shape/reason — the "load older" seam `TerminalViewModel` tests fake. */
interface TerminalScrollbackSource {
    suspend fun scrollback(name: String, lines: Int = 5000, plain: Boolean = false): ScrollbackResult
}

/**
 * The single call site into the generated mobile BFF client
 * (`:data:mobile-api-client`) for terminal session listing, WS ticket
 * issuance and scrollback — mirrors [com.vpsmanager.data.session.SessionRepository]'s
 * shape exactly. No other module may reference [MobileApi] or its generated
 * model types directly; callers only ever see the sealed results here (or,
 * for the ticket half, the narrower [TerminalTicketSource] interface this
 * class also implements).
 */
class TerminalRepository(
    private val mobileApi: MobileApi = MobileApi(),
    // The three session routes (kill, assign, peek) were born under the BFF's
    // `terminal` tag, so the generator put them in another class. Same base and
    // same authentication — just a different generated file.
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

    override suspend fun historico(name: String, bytes: Int): RawLogResult = try {
        val resposta = mobileApi.getTerminalHistorico(name = name, bytes = bytes.toLong())
        // The same hop off the caller's dispatcher that logBruto makes, and
        // for the same reason: decoding a few MiB of base64 on the main thread
        // is a visible freeze.
        withContext(Dispatchers.Default) {
            RawLogResult.Success(
                bytes = java.util.Base64.getDecoder().decode(resposta.base64),
                total = resposta.total.toInt(),
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

    override suspend fun logBruto(name: String, bytes: Int): RawLogResult = try {
        val resposta = mobileApi.getTerminalRawLog(name = name, bytes = bytes.toLong())
        // The generated client already switches to IO for the call, but comes
        // back to the caller's dispatcher BEFORE decoding — and here the
        // decoding is not cheap: up to 16 MiB of log becomes ~22 MiB of base64.
        // On the main thread that is a visible freeze of the screen. Move off.
        withContext(Dispatchers.Default) {
            RawLogResult.Success(
                // java.util's Base64 (not android.util's): minSdk 34
                // guarantees it on the device AND it exists on the JVM, so the
                // same path runs in unit tests, with no platform stand-in.
                bytes = java.util.Base64.getDecoder().decode(resposta.base64),
                total = resposta.total.toInt(),
            )
        }
    } catch (e: ClientException) {
        RawLogResult.Error("Could not load the history (error ${e.statusCode}).")
    } catch (e: ServerException) {
        RawLogResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        RawLogResult.Error("Connection failed while loading the history.")
    } catch (e: IllegalArgumentException) {
        // corrupt base64: better to open without history than to bring the screen down.
        RawLogResult.Error("The history arrived corrupted from the server.")
    } catch (e: IllegalStateException) {
        RawLogResult.Error("Configuration error while loading the history.")
    } catch (e: Exception) {
        RawLogResult.Error("Could not load the history.")
    }

    override suspend fun backups(): BackupsResult = try {
        val resposta = mobileApi.listTerminalBackups()
        if (resposta.isEmpty()) {
            BackupsResult.Empty
        } else {
            BackupsResult.Success(
                resposta.map { bk ->
                    SessionBackup(
                        id = bk.id,
                        criadoEm = bk.created,
                        origem = bk.source,
                        bytes = bk.bytes,
                        sessoes = bk.sessions.orEmpty().map {
                            BackupSession(nome = it.name, resumo = it.summary, linhas = it.lines.toInt())
                        },
                    )
                },
            )
        }
    } catch (e: Exception) {
        BackupsResult.Error(motivoDe(e, "load the backups"))
    }

    override suspend fun criarBackup(sessao: String?): AcaoResult = try {
        val r = mobileApi.createTerminalBackup(CreateBackupRequest(name = sessao))
        val n = r.count.toInt()
        AcaoResult.Ok(if (n == 1) "1 session saved." else "$n sessions saved.")
    } catch (e: IOException) {
        enfileirar(
            metodo = "POST",
            caminho = "/terminal/backups",
            corpoJson = corpoJson("name" to sessao),
            descricao = if (sessao == null) "backup of all sessions" else "backup of $sessao",
            acao = "save the backup",
        )
    } catch (e: Exception) {
        AcaoResult.Erro(motivoDe(e, "save the backup"))
    }

    override suspend fun restaurar(id: String, sessao: String?): AcaoResult = try {
        val r = mobileApi.restoreTerminalBackup(RestoreBackupRequest(id = id, name = sessao))
        val feitas = r.restored.toInt()
        val puladas = r.skipped.toInt()
        // "Skipped" almost always means "that name is already live", which is
        // the right behaviour (restoring does not overwrite a session in use).
        // Saying only "restored" would hide that, and the person would go
        // looking for what did not come back.
        val texto = when {
            feitas == 0 && puladas > 0 -> "Nothing restored — $puladas already existed or were over the limit."
            puladas > 0 -> "$feitas restored; $puladas skipped because they already exist."
            feitas == 1 -> "1 session restored."
            else -> "$feitas sessions restored."
        }
        AcaoResult.Ok(texto)
    } catch (e: IOException) {
        enfileirar(
            metodo = "POST",
            caminho = "/terminal/backups/restore",
            corpoJson = corpoJson("id" to id, "name" to sessao),
            descricao = if (sessao == null) "backup restore" else "restore of $sessao",
            acao = "restore the backup",
        )
    } catch (e: Exception) {
        AcaoResult.Erro(motivoDe(e, "restore the backup"))
    }

    override suspend fun excluirBackup(id: String, sessao: String?): AcaoResult = try {
        mobileApi.deleteTerminalBackup(id = id, name = sessao)
        AcaoResult.Ok(if (sessao == null) "Backup deleted." else "Session removed from the backup.")
    } catch (e: IOException) {
        enfileirar(
            metodo = "DELETE",
            // No body: the id goes in the path and the session in the query, as the route expects.
            caminho = "/terminal/backups/" + urlEncode(id) +
                (sessao?.let { "?name=" + urlEncode(it) } ?: ""),
            corpoJson = "",
            descricao = if (sessao == null) "backup deletion" else "removal of $sessao from the backup",
            acao = "delete the backup",
        )
    } catch (e: Exception) {
        AcaoResult.Erro(motivoDe(e, "delete the backup"))
    }

    override suspend fun renomearSessao(de: String, para: String): AcaoResult = try {
        mobileApi.renameTerminalSession(RenameSessionRequest(from = de, to = para))
        AcaoResult.Ok("Renamed to $para.")
    } catch (e: IOException) {
        enfileirar(
            metodo = "POST",
            caminho = "/terminal/sessions/rename",
            corpoJson = corpoJson("from" to de, "to" to para),
            descricao = "rename $de to $para",
            acao = "rename the session",
        )
    } catch (e: Exception) {
        AcaoResult.Erro(motivoDe(e, "rename the session"))
    }

    override suspend fun matarSessao(nome: String): AcaoResult = try {
        terminalApi.killTerminalSession(KillSessionRequest(name = nome))
        AcaoResult.Ok("Session $nome ended.")
    } catch (e: Exception) {
        AcaoResult.Erro(motivoDe(e, "end the session"))
    }

    override suspend fun atribuirSessao(nome: String, alvo: String): AcaoResult = try {
        terminalApi.assignTerminalSession(AssignSessionRequest(name = nome, target = alvo))
        // The sentence states the RESULT, not the action: whoever chose
        // "Everyone" needs to read that everyone can now see it, not
        // "assigned successfully".
        val quem = if (alvo == ALVO_TODOS) "everyone" else alvo
        AcaoResult.Ok("$nome is now visible to $quem.")
    } catch (e: IOException) {
        enfileirar(
            metodo = "POST",
            caminho = "/terminal/sessions/assign",
            corpoJson = corpoJson("name" to nome, "target" to alvo),
            descricao = "change who sees $nome",
            acao = "change who sees the session",
        )
    } catch (e: Exception) {
        AcaoResult.Erro(motivoDe(e, "change who sees the session"))
    }

    override suspend fun alvosDeAtribuicao(): AlvosResult = try {
        // The generator marks the list as nullable (the field is not required
        // in the schema). An absent list and an empty one amount to the same
        // thing for whoever is choosing: there is no target to offer, and the
        // screen disables the option instead of opening an empty sheet.
        AlvosResult.Success(terminalApi.listAssignTargets().targets.orEmpty())
    } catch (e: Exception) {
        AlvosResult.Error(motivoDe(e, "list who the session can be shown to"))
    }

    override suspend fun previaDaSessao(nome: String, linhas: Int): PreviaResult = try {
        val r = terminalApi.previewTerminalSession(name = nome, lines = linhas.toLong())
        PreviaResult.Success(texto = r.text, linhas = r.lines.toInt())
    } catch (e: Exception) {
        PreviaResult.Error(motivoDe(e, "preview the session"))
    }
}

/**
 * Stores the action for when the network comes back, or returns the usual error
 * if the queue cannot accept it (not installed, or at its ceiling).
 *
 * Only called from `catch (IOException)`: 4xx and 5xx remain immediate errors.
 * A queued 4xx would be repeated forever without changing its outcome, and a
 * 5xx is the server saying it heard — the queue is for those who were NOT
 * heard.
 *
 * The proof is [ProvaDeIdempotencia.CHAVE_NO_CABECALHO]: the five routes used
 * here declare `Idempotency-Key` on the BFF, and it is that table which
 * guarantees the retry does not execute twice.
 */
private fun enfileirar(
    metodo: String,
    caminho: String,
    corpoJson: String,
    descricao: String,
    acao: String,
): AcaoResult {
    val guardada = FilaDeEnvio.enfileirar(
        metodo = metodo,
        caminho = caminho,
        corpoJson = corpoJson,
        descricao = descricao,
        prova = ProvaDeIdempotencia.CHAVE_NO_CABECALHO,
    )
    return if (guardada) {
        AcaoResult.NaFila("No internet — $descricao is queued and goes out when the network is back.")
    } else {
        AcaoResult.Erro(motivoDe(IOException(), acao))
    }
}

/**
 * The body assembled by hand, as in the WhatsApp send and for the same reason:
 * the queue stores TEXT that has to survive closing the app, and the generated
 * client's object is neither serializable nor stable across OpenAPI
 * regenerations.
 *
 * A null field is OMITTED, never an explicit `null` — to the Go decoder on the
 * other side, an absent field and a `null` are not the same thing.
 */
private fun corpoJson(vararg campos: Pair<String, String?>): String =
    kotlinx.serialization.json.JsonObject(
        campos.filter { it.second != null }
            .associate { (chave, valor) -> chave to kotlinx.serialization.json.JsonPrimitive(valor) },
    ).toString()

private fun urlEncode(v: String): String = java.net.URLEncoder.encode(v, "UTF-8")

/** The target meaning "everyone" in the server's assignment contract. */
const val ALVO_TODOS: String = "*"

/**
 * The same exception ladder the other methods in this file repeat by hand, in
 * one place.
 *
 * Each type becomes a different sentence on purpose: "no network" and "server
 * down" lead the person to opposite actions, and a generic "it did not work"
 * would make the two look the same. The [acao] goes into the sentence ("could
 * not restore the backup") because the message appears far from the button that
 * caused it.
 */
private fun motivoDe(e: Exception, acao: String): String = when (e) {
    is ClientException -> when (e.statusCode) {
        404 -> "Not found — it may have been removed from another device."
        400 -> "The server rejected the request."
        else -> "Could not $acao (error ${e.statusCode})."
    }
    is ServerException -> "The server is unavailable right now."
    is IOException -> "Connection failed. Check your network and try again."
    else -> "Could not $acao."
}
