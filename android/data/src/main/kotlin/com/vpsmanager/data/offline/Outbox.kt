package com.vpsmanager.data.offline

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import java.io.File
import java.io.IOException
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import com.vpsmanager.mobileapiclient.infrastructure.ApiClient

/**
 * How the server recognises that it has already seen this action.
 * It is the condition for entering the queue; see [Outbox].
 */
enum class IdempotencyProof {
    /** The body carries an identifier the server deduplicates on (`client_msg_id` on a WhatsApp send). */
    IN_BODY,

    /** Applying the action twice leaves the same state as once (e.g. marking as read). */
    NATURALLY_REPEATABLE,

    /**
     * The server stores the first result under the `Idempotency-Key` header and
     * returns it on a retry without executing again. [OutboxWorker.send] puts
     * [PendingSend.id] on the wire; only use this for routes the server's
     * idempotency table actually covers.
     */
    KEY_IN_HEADER,
}

/**
 * An action the owner asked for that has not reached the server yet. [id] is
 * generated on the device and sent as the `Idempotency-Key`, so a retry after a
 * timeout cannot run the action twice.
 */
@Serializable
data class PendingSend(
    val id: String,
    @SerialName("metodo") val method: String,
    @SerialName("caminho") val path: String,
    @SerialName("corpoJson") val bodyJson: String,
    @SerialName("criadoEm") val createdAt: Long,
    @SerialName("descricao") val description: String,
    @SerialName("tentativas") val attempts: Int = 0,
)

/**
 * The queue of writes that go out when the network comes back (an outbox backed
 * by WorkManager, which survives app close and reboot and has a network
 * constraint built in). A read cache cannot do this: a write has no response to
 * store.
 *
 * A queue resends, and a timeout is indistinguishable from "never arrived", so an
 * action may only enter if the server can recognise a repeat. That is why
 * [enqueue] requires an [IdempotencyProof]. Opening the queue to more routes is a
 * server change (a real idempotency key on them), not a client one.
 *
 * - Order is preserved: FIFO, with a unique chained job.
 * - 4xx is not retried: the server understood and refused. Only network failures
 *   and 5xx go back on the queue.
 */
object Outbox {

    private const val FILE = "fila-de-envio.json"
    private const val WORK = "vpsm-fila-de-envio"

    /**
     * Above this the queue stops accepting. A hundred pending actions mean
     * something is wrong, and accepting more would promise things that may never happen.
     */
    private const val CAP = 100

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    /** Explicit: serializer inference does not reach through `List<T>` here. */
    private val SERIALIZER = ListSerializer(PendingSend.serializer())

    private val _pending = MutableStateFlow<List<PendingSend>>(emptyList())

    /** What has not gone out yet, shown in the UI so nothing is promised silently. */
    val pending: StateFlow<List<PendingSend>> = _pending.asStateFlow()

    private val _rejected = MutableStateFlow<List<PendingSend>>(emptyList())

    /**
     * Actions the server refused for good (4xx), which will never happen. The UI
     * must surface them, since the user was already told "queued", and call
     * [forgetRejections] once shown.
     */
    val rejected: StateFlow<List<PendingSend>> = _rejected.asStateFlow()

    @Volatile
    private var file: File? = null

    fun install(context: Context) {
        val app = context.applicationContext
        appContext = app
        val f = File(app.filesDir, FILE)
        file = f
        _pending.value = read(f)
        if (_pending.value.isNotEmpty()) schedule(app)
    }

    /**
     * The application context stored by [install]. Holding it in an `object` does
     * not leak because it lives as long as the process.
     */
    @Volatile
    private var appContext: Context? = null

    /**
     * Queues a write using the context stored by [install], so repositories that
     * detect a network failure need no `Context`. Returns `false` if the queue is
     * not installed yet.
     */
    fun enqueue(
        method: String,
        path: String,
        bodyJson: String,
        description: String,
        proof: IdempotencyProof,
    ): Boolean {
        val ctx = appContext ?: return false
        return enqueue(ctx, method, path, bodyJson, description, proof)
    }
    /**
     * Queues a write and returns `true` if it was accepted. Returns `false` when the
     * queue is full ([CAP]) or not installed; the caller must say so on screen.
     *
     * [proof] has no default on purpose: the caller must answer how the server
     * recognises a repeat.
     */
    fun enqueue(
        context: Context,
        method: String,
        path: String,
        bodyJson: String,
        description: String,
        proof: IdempotencyProof,
    ): Boolean {
        val f = file ?: return false
        val current = _pending.value
        if (current.size >= CAP) return false
        val next = current + PendingSend(
            id = UUID.randomUUID().toString(),
            method = method,
            path = path,
            bodyJson = bodyJson,
            createdAt = System.currentTimeMillis(),
            description = description,
        )
        _pending.value = next
        persist(f, next)
        schedule(context.applicationContext)
        return true
    }

    /** Removes the first item; call only after the server has accepted it. */
    internal fun completeFirst() {
        val f = file ?: return
        val remaining = _pending.value.drop(1)
        _pending.value = remaining
        persist(f, remaining)
    }

    /** Discards the first item after a definitive refusal (4xx) and returns it. */
    internal fun dropFirst(): PendingSend? {
        val first = _pending.value.firstOrNull() ?: return null
        completeFirst()
        return first
    }

    /** Discards the first item and keeps it as a warning for the UI. Called by the worker on a 4xx. */
    internal fun recordRejection() {
        val rejectedItem = dropFirst() ?: return
        _rejected.value = _rejected.value + rejectedItem
    }

    /** Clears the warnings that have already been shown. */
    fun forgetRejections() {
        _rejected.value = emptyList()
    }

    internal fun first(): PendingSend? = _pending.value.firstOrNull()

    private fun schedule(context: Context) {
        val request = OneTimeWorkRequestBuilder<OutboxWorker>()
            .setConstraints(
                Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build(),
            )
            // Exponential from 30 s: a network that just came back may still be
            // unstable (e.g. a captive portal).
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(context)
            // APPEND, never REPLACE: the order of the writes is part of their meaning.
            .enqueueUniqueWork(WORK, ExistingWorkPolicy.APPEND_OR_REPLACE, request)
    }

    private fun read(f: File): List<PendingSend> = runCatching {
        if (!f.exists()) emptyList() else json.decodeFromString(SERIALIZER, f.readText())
    }.getOrDefault(emptyList())

    private fun persist(f: File, list: List<PendingSend>) {
        runCatching { f.writeText(json.encodeToString(SERIALIZER, list)) }
    }

    internal fun resetForTest(f: File?) {
        file = f
        _pending.value = f?.let { read(it) } ?: emptyList()
        _rejected.value = emptyList()
    }

    /** The header name, written once. Matches the BFF's. */
    const val IDEMPOTENCY_HEADER: String = "Idempotency-Key"
}

/**
 * Delivers the queue one action at a time, in order: two writes on the same
 * resource arriving out of order would produce a state nobody asked for.
 */
class OutboxWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        var delivered = 0
        while (true) {
            val item = Outbox.first() ?: break
            when (send(item)) {
                Outcome.ACCEPTED -> {
                    Outbox.completeFirst()
                    delivered++
                }
                // Definitive refusal: retrying cannot change the outcome, so
                // record it for the UI instead of dropping it silently.
                Outcome.REJECTED -> Outbox.recordRejection()
                // Network or 5xx: the item stays and WorkManager retries with backoff.
                Outcome.RETRY_LATER -> return@withContext Result.retry()
            }
        }
        Result.success()
    }

    private enum class Outcome { ACCEPTED, REJECTED, RETRY_LATER }

    private fun send(item: PendingSend): Outcome {
        val base = ApiClient.baseUrlFromSystemProperty() ?: return Outcome.RETRY_LATER
        // No body when there is none: DELETE carries everything in the path and
        // query. POST/PUT/PATCH get `{}` because OkHttp throws on a null body for
        // them, which would crash the worker and stall the whole queue.
        val type = "application/json".toMediaType()
        val needsBody = item.method in setOf("POST", "PUT", "PATCH")
        val body = when {
            item.bodyJson.isNotBlank() -> item.bodyJson.toRequestBody(type)
            needsBody -> "{}".toRequestBody(type)
            else -> null
        }
        val req = Request.Builder()
            .url(base.trimEnd('/') + item.path)
            .method(item.method, body)
            // The id is generated once at queueing time and persisted, so a resend
            // after a timeout carries the SAME key.
            .header(Outbox.IDEMPOTENCY_HEADER, item.id)
            .build()
        return try {
            ApiClient.defaultClient.newCall(req).execute().use { r ->
                when {
                    r.isSuccessful -> Outcome.ACCEPTED
                    r.code in 400..499 -> Outcome.REJECTED
                    else -> Outcome.RETRY_LATER
                }
            }
        } catch (e: IOException) {
            Outcome.RETRY_LATER
        }
    }
}

/** The `basePath` published by the server configuration, or `null` before it. */
private fun ApiClient.Companion.baseUrlFromSystemProperty(): String? =
    System.getProperty(ApiClient.BASE_URL_KEY)
