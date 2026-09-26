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
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import com.vpsmanager.mobileapiclient.infrastructure.ApiClient

/**
 * An action the owner asked for that has not yet reached the server.
 *
 * [id] is generated on the device and travels in the `Idempotency-Key` header:
 * without it, a retry after a timeout could run the same action twice — and
 * "restart the container" twice is not the same as once.
 */
/**
 * How the server recognises that it has already seen this action.
 *
 * Not metadata: it is the condition for entering the queue. See [FilaDeEnvio]'s
 * KDoc.
 */
enum class ProvaDeIdempotencia {
    /**
     * The body already carries an identifier the server deduplicates on —
     * today, only `client_msg_id` on a WhatsApp send.
     */
    NO_CORPO,

    /**
     * The action is naturally repeatable: applying it twice leaves the same
     * state as applying it once (marking as read, for instance). No key needed.
     */
    NATURALMENTE_REPETIVEL,

    /**
     * The server stores the result of the first execution under the key sent
     * in the `Idempotency-Key` header, and returns that same result on a retry
     * — without executing again.
     *
     * This is the proof that holds for the terminal's writes (rename, back up,
     * restore, delete, assign). The key is the [EnvioPendente.id], and what
     * puts it on the wire is [TrabalhoDeEnvio.enviar]: queueing a route the
     * server does NOT cover with its idempotency table is not protected by this
     * value — it describes the server, not the caller's wishes.
     */
    CHAVE_NO_CABECALHO,
}

@Serializable
data class EnvioPendente(
    val id: String,
    val metodo: String,
    val caminho: String,
    val corpoJson: String,
    val criadoEm: Long,
    val descricao: String,
    val tentativas: Int = 0,
)

/**
 * The queue of writes that go out when the network comes back.
 *
 * ## Why the read cache did not solve this
 *
 * A cache serves a stored response; a write has no response to store. Serving
 * a `POST` from the cache would be **inventing that something happened** — the
 * opposite of what you want. The two halves of offline are different
 * mechanisms by nature: reading needs memory, writing needs a queue.
 *
 * ## The pattern, and why it is this one
 *
 * It is the *outbox* — the action is recorded locally and confirmed to the
 * owner immediately, and a background job delivers it once there is a network.
 * It is what Android's official offline-first guide calls a queue of pending
 * operations, and what WorkManager exists to do: it survives closing the app
 * and rebooting the device, and has a network constraint built in.
 *
 * ## The rule that decides what MAY enter, and why
 *
 * A queue resends. Resending is only safe if the server can recognise that it
 * has already seen that action — because **a timeout is indistinguishable from
 * "it never arrived"**, and without that distinction "restart the container"
 * can happen twice.
 *
 * Today the BFF proves idempotency on exactly ONE path: sending a WhatsApp
 * message, via `client_msg_id` in the body
 * (`internal/mobilebff/handlers_whatsapp.go`). There is no generic
 * `Idempotency-Key` support — such a header would be ignored by every other
 * route, and the queue would be betting that no retry ever duplicates.
 *
 * That is why [enfileirar] **requires** the caller to state what the proof is
 * ([ProvaDeIdempotencia]), and refuses anything without one. The refusal is
 * deliberate and loud: better for the screen to say "this needs a connection"
 * than for the app to promise an action that might run twice.
 *
 * Opening the queue to the rest of the app is a SERVER change — giving the
 * mutating routes a real idempotency key — not a client one.
 *
 * ## The other two rules
 *
 * - **Order is preserved.** FIFO, and the job is unique and chained. Two
 *   actions on the same resource, out of order, produce a state nobody asked
 *   for.
 * - **4xx is not retried.** The server understood and refused; insisting only
 *   burns battery. Only NETWORK failures and 5xx go back on the queue.
 */
object FilaDeEnvio {

    private const val ARQUIVO = "fila-de-envio.json"
    private const val TRABALHO = "vpsm-fila-de-envio"

    /**
     * Above this, the queue stops accepting. Not a disk limit — an honesty
     * limit: a hundred pending actions mean something is wrong (network dead
     * for days, server down), and carrying on accepting would have the app
     * promise a hundred things that may never happen.
     */
    private const val TETO = 100

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    /** Explicit: serializer inference does not reach through `List<T>` here. */
    private val SERIALIZADOR = ListSerializer(EnvioPendente.serializer())

    private val _pendentes = MutableStateFlow<List<EnvioPendente>>(emptyList())

    /** What has not gone out yet. The interface shows this so as not to promise in silence. */
    val pendentes: StateFlow<List<EnvioPendente>> = _pendentes.asStateFlow()

    private val _recusadas = MutableStateFlow<List<EnvioPendente>>(emptyList())

    /**
     * What the server refused for good (4xx) and which therefore will NEVER
     * happen.
     *
     * It exists because the discard used to be silent: the action left the
     * queue and the person — who had already read "queued, goes out when the
     * internet is back" — never found out. A queue that swallows the refusal is
     * worse than no queue at all, because it promised.
     *
     * Whoever displays this must call [esquecerRecusas] when they do: it is a
     * warning to be read once, not a history.
     */
    val recusadas: StateFlow<List<EnvioPendente>> = _recusadas.asStateFlow()

    @Volatile
    private var arquivo: File? = null

    fun instalar(context: Context) {
        val app = context.applicationContext
        appContext = app
        val f = File(app.filesDir, ARQUIVO)
        arquivo = f
        _pendentes.value = ler(f)
        if (_pendentes.value.isNotEmpty()) agendar(app)
    }

    /**
     * The application context, stored by [instalar].
     *
     * Keeping a `Context` in an `object` is the classic leak recipe — and here
     * it is not, for one reason only: it is the `applicationContext`, which
     * lives as long as the process. Keeping an Activity here would leak a whole
     * screen; keeping the application's leaks nothing, because there is nothing
     * longer-lived to leak INTO.
     */
    @Volatile
    private var appContext: Context? = null

    /**
     * Queues a write and returns `true` if it was accepted.
     *
     * It refuses (returns `false`) in two cases, and the caller has to SAY so
     * on screen — an action refused in silence is worse than a visible network
     * error:
     *
     * - the queue is at its ceiling ([TETO]);
     * - the queue has not been installed yet.
     *
     * [prova] deliberately has no default value: it forces whoever writes the
     * caller to stop and answer "how does the server know it has seen this
     * already?". A default here would let the question be skipped, which is
     * exactly how an action becomes a double.
     */
    /**
     * Queues WITHOUT being handed a context — using what [instalar] stored.
     *
     * It exists because the one who finds out the network dropped is the
     * REPOSITORY, inside a `catch (e: IOException)`, and a repository has no
     * context and should not be given one just to schedule work. The
     * alternative was carrying a `Context` through three layers (screen ->
     * ViewModel -> repository) to reach a `WorkManager.getInstance` — pure
     * plumbing, and the kind of plumbing that makes a person give up on wiring
     * the queue at all. Which is exactly what happened: the queue sat finished
     * and SWITCHED OFF for a whole session.
     *
     * Returns `false` if the queue has not been installed yet. It does not
     * throw: a message that cannot be queued becomes a visible failure on
     * screen, which is already the previous behaviour.
     */
    fun enfileirar(
        metodo: String,
        caminho: String,
        corpoJson: String,
        descricao: String,
        prova: ProvaDeIdempotencia,
    ): Boolean {
        val ctx = appContext ?: return false
        return enfileirar(ctx, metodo, caminho, corpoJson, descricao, prova)
    }

    fun enfileirar(
        context: Context,
        metodo: String,
        caminho: String,
        corpoJson: String,
        descricao: String,
        prova: ProvaDeIdempotencia,
    ): Boolean {
        val f = arquivo ?: return false
        val atual = _pendentes.value
        if (atual.size >= TETO) return false
        val nova = atual + EnvioPendente(
            id = UUID.randomUUID().toString(),
            metodo = metodo,
            caminho = caminho,
            corpoJson = corpoJson,
            criadoEm = System.currentTimeMillis(),
            descricao = descricao,
        )
        _pendentes.value = nova
        gravar(f, nova)
        agendar(context.applicationContext)
        return true
    }

    /** Removes the first item — called only after the server has accepted. */
    internal fun concluirPrimeiro() {
        val f = arquivo ?: return
        val restante = _pendentes.value.drop(1)
        _pendentes.value = restante
        gravar(f, restante)
    }

    /** Descarta o primeiro por recusa definitiva (4xx), registrando o motivo. */
    internal fun descartarPrimeiro(): EnvioPendente? {
        val primeiro = _pendentes.value.firstOrNull() ?: return null
        concluirPrimeiro()
        return primeiro
    }

    /**
     * Discards the first item AND keeps the warning for the interface. It is
     * what the worker calls on a 4xx — [descartarPrimeiro] alone returned the
     * item to a caller that threw the return value away.
     */
    internal fun registrarRecusa() {
        val recusada = descartarPrimeiro() ?: return
        _recusadas.value = _recusadas.value + recusada
    }

    /** Clears the warnings that have already been shown. */
    fun esquecerRecusas() {
        _recusadas.value = emptyList()
    }

    internal fun primeiro(): EnvioPendente? = _pendentes.value.firstOrNull()

    private fun agendar(context: Context) {
        val pedido = OneTimeWorkRequestBuilder<TrabalhoDeEnvio>()
            .setConstraints(
                Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build(),
            )
            // Exponential from 30 s: a network that has just come back may be
            // unstable (a captive portal half-way there), and hammering the
            // server every second does not make the action arrive any sooner.
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(context)
            // APPEND, never REPLACE: the order of the writes is part of what
            // they mean.
            .enqueueUniqueWork(TRABALHO, ExistingWorkPolicy.APPEND_OR_REPLACE, pedido)
    }

    private fun ler(f: File): List<EnvioPendente> = runCatching {
        if (!f.exists()) emptyList() else json.decodeFromString(SERIALIZADOR, f.readText())
    }.getOrDefault(emptyList())

    private fun gravar(f: File, lista: List<EnvioPendente>) {
        runCatching { f.writeText(json.encodeToString(SERIALIZADOR, lista)) }
    }

    internal fun reiniciarParaTeste(f: File?) {
        arquivo = f
        _pendentes.value = f?.let { ler(it) } ?: emptyList()
        _recusadas.value = emptyList()
    }

    /** The header name, written once. Matches the BFF's. */
    const val CABECALHO_IDEMPOTENCIA: String = "Idempotency-Key"
}

/**
 * Delivers the queue, one action at a time, in order.
 *
 * ## Why one at a time
 *
 * Parallelising would be faster and wrong: two writes on the same resource
 * arriving out of order produce a final state nobody asked for. The queue is
 * small by nature (actions the owner triggered by hand), so serialising costs
 * nothing noticeable.
 */
class TrabalhoDeEnvio(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        var entregues = 0
        while (true) {
            val item = FilaDeEnvio.primeiro() ?: break
            when (enviar(item)) {
                Desfecho.ACEITO -> {
                    FilaDeEnvio.concluirPrimeiro()
                    entregues++
                }
                // A definitive refusal: the server understood and said no.
                // Insisting would burn battery forever without changing the
                // outcome — but vanishing in silence is worse: the person asked
                // for a rename, saw "queued", and would never learn it did not
                // happen.
                Desfecho.RECUSADO -> FilaDeEnvio.registrarRecusa()
                // Network or 5xx: comes back later, on WorkManager's backoff.
                // The queue STAYS — which is precisely what makes it a queue.
                Desfecho.TENTAR_DEPOIS -> return@withContext Result.retry()
            }
        }
        Result.success()
    }

    private enum class Desfecho { ACEITO, RECUSADO, TENTAR_DEPOIS }

    private fun enviar(item: EnvioPendente): Desfecho {
        val base = ApiClient.baseUrlFromSystemProperty() ?: return Desfecho.TENTAR_DEPOIS
        // A null body when there is none: DELETE carries everything in the
        // path and the query, and sending `""` as JSON would make the server
        // refuse a body nobody asked for.
        //
        // The `{}` in the middle is there for a case that would only show up in
        // production: OkHttp REQUIRES a body on POST/PUT/PATCH and throws if
        // given null. An item of those families queued without a body would
        // bring the worker down — and the worker is what drains the queue,
        // which means ALL the stored actions would stall, not just the faulty
        // one.
        val tipo = "application/json".toMediaType()
        val exigeCorpo = item.metodo in setOf("POST", "PUT", "PATCH")
        val corpo = when {
            item.corpoJson.isNotBlank() -> item.corpoJson.toRequestBody(tipo)
            exigeCorpo -> "{}".toRequestBody(tipo)
            else -> null
        }
        val req = Request.Builder()
            .url(base.trimEnd('/') + item.caminho)
            .method(item.metodo, corpo)
            // The key that keeps a retry from executing twice. The item's id
            // is generated once, at queueing time, and survives a process
            // restart along with the rest of the item — resending AFTER a
            // timeout sends the SAME key, which is the only reason it exists.
            // Until this line appeared, the `id` never left the device.
            .header(FilaDeEnvio.CABECALHO_IDEMPOTENCIA, item.id)
            .build()
        return try {
            ApiClient.defaultClient.newCall(req).execute().use { r ->
                when {
                    r.isSuccessful -> Desfecho.ACEITO
                    r.code in 400..499 -> Desfecho.RECUSADO
                    else -> Desfecho.TENTAR_DEPOIS
                }
            }
        } catch (e: IOException) {
            Desfecho.TENTAR_DEPOIS
        }
    }
}

/** The `basePath` published by the server configuration, or `null` before it. */
private fun ApiClient.Companion.baseUrlFromSystemProperty(): String? =
    System.getProperty(ApiClient.BASE_URL_KEY)
