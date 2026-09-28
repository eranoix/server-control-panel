package dev.servercontrolpanel.data.offline

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
import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient

enum class IdempotencyProof {
    IN_BODY,

    NATURALLY_REPEATABLE,

    KEY_IN_HEADER,
}

@Serializable
data class PendingSend(
    val id: String,
    @SerialName("method") val method: String,
    @SerialName("path") val path: String,
    @SerialName("bodyJson") val bodyJson: String,
    @SerialName("createdAt") val createdAt: Long,
    @SerialName("description") val description: String,
    @SerialName("attempts") val attempts: Int = 0,
)

object Outbox {

    private const val FILE = "send-queue.json"
    private const val WORK = "panel-send-queue"

    private const val CAP = 100

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    private val SERIALIZER = ListSerializer(PendingSend.serializer())

    private val _pending = MutableStateFlow<List<PendingSend>>(emptyList())

    val pending: StateFlow<List<PendingSend>> = _pending.asStateFlow()

    private val _rejected = MutableStateFlow<List<PendingSend>>(emptyList())

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

    @Volatile
    private var appContext: Context? = null

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

    internal fun completeFirst() {
        val f = file ?: return
        val remaining = _pending.value.drop(1)
        _pending.value = remaining
        persist(f, remaining)
    }

    internal fun dropFirst(): PendingSend? {
        val first = _pending.value.firstOrNull() ?: return null
        completeFirst()
        return first
    }

    internal fun recordRejection() {
        val rejectedItem = dropFirst() ?: return
        _rejected.value = _rejected.value + rejectedItem
    }

    fun forgetRejections() {
        _rejected.value = emptyList()
    }

    internal fun first(): PendingSend? = _pending.value.firstOrNull()

    private fun schedule(context: Context) {
        val request = OneTimeWorkRequestBuilder<OutboxWorker>()
            .setConstraints(
                Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build(),
            )
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(context)
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

    const val IDEMPOTENCY_HEADER: String = "Idempotency-Key"
}

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
                Outcome.REJECTED -> Outbox.recordRejection()
                Outcome.RETRY_LATER -> return@withContext Result.retry()
            }
        }
        Result.success()
    }

    private enum class Outcome { ACCEPTED, REJECTED, RETRY_LATER }

    private fun send(item: PendingSend): Outcome {
        val base = ApiClient.baseUrlFromSystemProperty() ?: return Outcome.RETRY_LATER
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

private fun ApiClient.Companion.baseUrlFromSystemProperty(): String? =
    System.getProperty(ApiClient.BASE_URL_KEY)
