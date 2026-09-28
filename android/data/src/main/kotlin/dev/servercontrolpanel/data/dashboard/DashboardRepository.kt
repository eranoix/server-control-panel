package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.OpsRepository
import dev.servercontrolpanel.data.ops.OpsSource
import dev.servercontrolpanel.data.ops.OpsStatusResult
import dev.servercontrolpanel.data.sdui.SduiDataClient
import dev.servercontrolpanel.data.session.SessionRepository
import dev.servercontrolpanel.data.session.SessionResult
import dev.servercontrolpanel.data.session.SessionSource
import dev.servercontrolpanel.mobileapiclient.infrastructure.RequestMethod
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonObject

sealed interface DashboardResult {
    data class Success(val snapshot: DashboardSnapshot) : DashboardResult

    data class Error(val reason: String) : DashboardResult
}

interface DashboardSource {
    suspend fun load(): DashboardResult
}

private const val DEPLOY_APPS_ENDPOINT = "/api/mobile/v1/deploy/apps"
private const val SCHEDULER_JOBS_ENDPOINT = "/api/mobile/v1/scheduler/jobs"

class DashboardRepository(
    private val ops: OpsSource = OpsRepository(),
    private val session: SessionSource = SessionRepository(),
    private val rows: RowsSource = SduiRowsSource(),
    private val now: () -> Long = System::currentTimeMillis,
) : DashboardSource {

    override suspend fun load(): DashboardResult = coroutineScope {
        val opsDeferred = async { ops.fetchStatus() }
        val sessionDeferred = async { session.getMe() }
        val deployDeferred = async { rows.fetch(DEPLOY_APPS_ENDPOINT) }
        val schedulerDeferred = async { rows.fetch(SCHEDULER_JOBS_ENDPOINT) }

        val opsResult = opsDeferred.await()
        val sessionResult = sessionDeferred.await()
        val deployRows = deployDeferred.await()
        val schedulerRows = schedulerDeferred.await()

        when (opsResult) {
            is OpsStatusResult.Error -> DashboardResult.Error(opsResult.reason)
            is OpsStatusResult.Success -> DashboardResult.Success(
                DashboardSnapshot(
                    ops = opsResult.snapshot,
                    identity = (sessionResult as? SessionResult.Success)?.let {
                        DashboardIdentity(user = it.user, email = it.email, isAdmin = it.isAdmin)
                    },
                    deploys = deployRows?.map(::toDeploySummary),
                    scheduled = schedulerRows?.map(::toScheduledSummary),
                    fetchedAtEpochMs = now(),
                ),
            )
        }
    }
}

interface RowsSource {
    suspend fun fetch(endpoint: String): List<JsonObject>?
}

class SduiRowsSource(
    private val client: SduiDataClient = SduiDataClient(),
) : RowsSource {
    override suspend fun fetch(endpoint: String): List<JsonObject>? = try {
        val body = client.call(endpoint, RequestMethod.GET)
        (body as? JsonObject)?.get("rows")?.let { it as? JsonArray }?.mapNotNull { row ->
            row as? JsonObject
        }
    } catch (e: Exception) {
        null
    }
}

private fun JsonObject.text(key: String): String =
    (this[key] as? JsonPrimitive)?.content.orEmpty()

private fun JsonObject.flag(key: String): Boolean =
    (this[key] as? JsonPrimitive)?.booleanOrNull ?: false

private fun toDeploySummary(row: JsonObject) = DeploySummary(
    name = row.text("name").ifBlank { row.text("id") },
    lastStatus = row.text("last_status"),
    updated = row.text("updated"),
)

private fun toScheduledSummary(row: JsonObject) = ScheduledSummary(
    name = row.text("name").ifBlank { row.text("id") },
    lastStatus = row.text("last_status"),
    lastFire = row.text("last_fire"),
    nextFire = row.text("next_fire"),
    enabled = row.flag("enabled"),
)

internal fun JsonElement.asRow(): JsonObject = jsonObject
