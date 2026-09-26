package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.OpsRepository
import com.vpsmanager.data.ops.OpsSource
import com.vpsmanager.data.ops.OpsStatusResult
import com.vpsmanager.data.sdui.SduiDataClient
import com.vpsmanager.data.session.SessionRepository
import com.vpsmanager.data.session.SessionResult
import com.vpsmanager.data.session.SessionSource
import com.vpsmanager.mobileapiclient.infrastructure.RequestMethod
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

    /** Only happens when `/ops/status` — the one indispensable call — fails. */
    data class Error(val reason: String) : DashboardResult
}

/**
 * The seam the Home ViewModel depends on instead of touching the generated
 * client — same convention as [OpsSource]/[SessionSource].
 */
interface DashboardSource {
    suspend fun load(): DashboardResult
}

/** Row endpoints the Home screen reads. Both inside the mobile BFF namespace. */
private const val DEPLOY_APPS_ENDPOINT = "/api/mobile/v1/deploy/apps"
private const val SCHEDULER_JOBS_ENDPOINT = "/api/mobile/v1/scheduler/jobs"

/**
 * Assembles the Home dashboard.
 *
 * ## Why four calls and not one
 * The ideal would be one fat `/ops/status` bringing everything — this is an opening
 * screen, and every round trip on a mobile network is one more chance of half a screen.
 * Today `/ops/status` already brings health, queue, alerts and resources in a single
 * call; what is missing (last deploy, scheduled jobs) only exists in the row endpoints,
 * and extending the server is out of scope for this screen. So: the four go out IN
 * PARALLEL ([coroutineScope] + [async]), and the wall-clock cost is that of the
 * slowest, not the sum.
 *
 * ## Degrading part by part
 * `/ops/status` is the only mandatory one: without it there is no dashboard, and the
 * result is [DashboardResult.Error] with what to do about it. The other three are
 * best effort — failing on `/deploy/apps` leaves that piece NULL (the card says "I
 * could not find out") and does not bring down the whole screen. Half a screen that
 * is true beats a full screen that is invented.
 */
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

/**
 * Fetches the rows of a `{"rows":[…]}` endpoint of the BFF. It exists as an
 * interface so the repository test needs no server — the real client is
 * [SduiRowsSource].
 *
 * Returns `null` on any failure: the caller treats "I do not know" and "there
 * are none" as different things.
 */
interface RowsSource {
    suspend fun fetch(endpoint: String): List<JsonObject>?
}

/**
 * Reuses [SduiDataClient] — the app's only client able to call a BFF path chosen
 * at run time. The generated methods for these two endpoints do exist, but they
 * declare `schema: {}` in the contract and therefore return `kotlin.Any`: a type
 * serialization cannot decode. The raw JSON route is what is already in
 * production for the whole SDUI surface.
 */
class SduiRowsSource(
    private val client: SduiDataClient = SduiDataClient(),
) : RowsSource {
    override suspend fun fetch(endpoint: String): List<JsonObject>? = try {
        val body = client.call(endpoint, RequestMethod.GET)
        (body as? JsonObject)?.get("rows")?.let { it as? JsonArray }?.mapNotNull { row ->
            row as? JsonObject
        }
    } catch (e: Exception) {
        // Best effort by contract: no failure from here may bring down the
        // dashboard, and "I do not know" is the honest result for any of them.
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

/** Test convenience: a raw row built from any [JsonElement]. */
internal fun JsonElement.asRow(): JsonObject = jsonObject
