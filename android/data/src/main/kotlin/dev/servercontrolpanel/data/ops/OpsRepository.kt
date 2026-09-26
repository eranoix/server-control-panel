package dev.servercontrolpanel.data.ops

import dev.servercontrolpanel.mobileapiclient.api.MobileApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.AlertSummary
import dev.servercontrolpanel.mobileapiclient.model.OpsStatus
import dev.servercontrolpanel.mobileapiclient.model.TriggerDeployRequest
import java.io.IOException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement

/** One currently-firing alert rule, screen-shaped straight from `GET /api/mobile/v1/ops/status`. */
data class OpsAlert(
    val name: String,
    val severity: String,
    val state: String,
    val currentValue: Double,
    val threshold: Double,
    val unit: String?,
    val firedSinceEpoch: Long? = null,
)

/**
 * The full `GET /api/mobile/v1/ops/status` snapshot — health checks, queue depth, fired alerts
 * and, more recently, the machine's resources in [system].
 *
 * [system] is nullable because the app runs against servers that may be older than it is: a
 * BFF without the field returns `null`, and the Home screen hides the resources card instead of
 * drawing zero — an invented number on an operations dashboard is worse than a missing field.
 */
data class OpsSnapshot(
    val health: Map<String, String>,
    val healthOk: Boolean,
    val queueRunning: Long,
    val queueQueued: Long,
    val alerts: List<OpsAlert>,
    val system: SystemSnapshot? = null,
)

sealed interface OpsStatusResult {
    data class Success(val snapshot: OpsSnapshot) : OpsStatusResult
    data class Error(val reason: String) : OpsStatusResult
}

sealed interface TriggerDeployResult {
    data class Success(val jobId: String) : TriggerDeployResult
    data class Error(val reason: String) : TriggerDeployResult
}

/** One `GET /api/mobile/v1/ops/deploy/{jobID}` poll — the fallback paint when the socket is down. */
data class DeployStatus(
    val status: String,
    val progress: Long,
    val step: String?,
    val error: String?,
)

sealed interface DeployStatusResult {
    data class Success(val status: DeployStatus) : DeployStatusResult
    data class Error(val reason: String) : DeployStatusResult
}

/**
 * One `deploy.<jobID>` live event (`internal/queue.Event`, forwarded verbatim by
 * `events_bridge_ops.go`): `type` is one of `"status"`/`"progress"`/`"step"`/`"log"` —
 * `OpsDashboardViewModel`/`DeployTriggerViewModel` never invent a fifth kind, they render
 * whichever of the optional fields is present for the event's `type`.
 */
@Serializable
data class DeployLogEvent(
    val type: String,
    @SerialName("job_id") val jobId: String,
    val status: String? = null,
    val progress: Int = 0,
    val step: String? = null,
    @SerialName("log") val logLine: String? = null,
    val ts: Long = 0,
)

private val eventJson = Json { ignoreUnknownKeys = true }

private fun OpsStatus.toSnapshot(): OpsSnapshot = OpsSnapshot(
    health = health,
    healthOk = healthOk,
    queueRunning = queueRunning,
    queueQueued = queueQueued,
    alerts = (alerts ?: emptyList()).map(AlertSummary::toOpsAlert),
    system = system?.toSnapshot(),
)

private fun AlertSummary.toOpsAlert() = OpsAlert(
    name = name,
    severity = severity,
    state = state,
    currentValue = currentValue,
    threshold = threshold,
    unit = unit,
    firedSinceEpoch = firedSince,
)

/** Decodes a `MobileEvent.data` payload delivered on the `"ops.health"` channel. */
fun decodeOpsSnapshot(data: JsonElement): OpsSnapshot? = try {
    eventJson.decodeFromJsonElement(OpsStatus.serializer(), data).toSnapshot()
} catch (e: Exception) {
    null
}

/** Decodes a `MobileEvent.data` payload delivered on a `"deploy.<jobID>"` channel. */
fun decodeDeployEvent(data: JsonElement): DeployLogEvent? = try {
    eventJson.decodeFromJsonElement(DeployLogEvent.serializer(), data)
} catch (e: Exception) {
    null
}

/**
 * Abstraction `OpsDashboardViewModel`/`DeployTriggerViewModel` (`:feature-admin`) depend on, so
 * their unit tests supply a fake instead of touching the generated mobile BFF client directly
 * same convention as [dev.servercontrolpanel.data.push.NotifyPreferencesSource].
 */
interface OpsSource {
    suspend fun fetchStatus(): OpsStatusResult
    suspend fun triggerDeploy(): TriggerDeployResult
    suspend fun fetchDeployStatus(jobId: String): DeployStatusResult
}

/**
 * The single call site into the generated mobile BFF client for `/ops/status` and
 * `/ops/deploy` — mirrors [dev.servercontrolpanel.data.push.NotifyPreferencesRepository]'s shape.
 * [triggerDeploy] always sends `confirm: true`: the caller (a confirmed dialog in
 * `DeployTriggerScreen`) is the only place this function is ever invoked from, so there is
 * no path in this repository that can send `confirm: false` or omit it.
 */
class OpsRepository(
    private val mobileApi: MobileApi = MobileApi(),
) : OpsSource {
    override suspend fun fetchStatus(): OpsStatusResult = try {
        OpsStatusResult.Success(mobileApi.getOpsStatus().toSnapshot())
    } catch (e: ClientException) {
        OpsStatusResult.Error("Could not load the operational status (error ${e.statusCode}).")
    } catch (e: ServerException) {
        OpsStatusResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        OpsStatusResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        OpsStatusResult.Error("Configuration error while loading the operational status.")
    } catch (e: UnsupportedOperationException) {
        OpsStatusResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        OpsStatusResult.Error("Could not load the operational status.")
    }

    override suspend fun triggerDeploy(): TriggerDeployResult = try {
        val response = mobileApi.triggerSelfDeploy(TriggerDeployRequest(confirm = true))
        TriggerDeployResult.Success(jobId = response.jobId)
    } catch (e: ClientException) {
        TriggerDeployResult.Error("Could not trigger the deploy (error ${e.statusCode}).")
    } catch (e: ServerException) {
        TriggerDeployResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        TriggerDeployResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        TriggerDeployResult.Error("Configuration error while triggering the deploy.")
    } catch (e: UnsupportedOperationException) {
        TriggerDeployResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        TriggerDeployResult.Error("Could not trigger the deploy.")
    }

    override suspend fun fetchDeployStatus(jobId: String): DeployStatusResult = try {
        val response = mobileApi.getSelfDeployStatus(jobId)
        DeployStatusResult.Success(
            DeployStatus(
                status = response.status,
                progress = response.progress,
                step = response.step,
                error = response.error,
            ),
        )
    } catch (e: ClientException) {
        DeployStatusResult.Error("Could not load the deploy status (error ${e.statusCode}).")
    } catch (e: ServerException) {
        DeployStatusResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        DeployStatusResult.Error("Connection failed. Check your network and try again.")
    } catch (e: IllegalStateException) {
        DeployStatusResult.Error("Configuration error while loading the deploy status.")
    } catch (e: UnsupportedOperationException) {
        DeployStatusResult.Error("Unexpected response from the server.")
    } catch (e: Exception) {
        DeployStatusResult.Error("Could not load the deploy status.")
    }
}
