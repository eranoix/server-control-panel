package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.OpsSnapshot

/** Who is signed in, for the identity footer. */
data class DashboardIdentity(
    val user: String,
    val email: String,
    val isAdmin: Boolean,
)

/** One row of `GET /deploy/apps`. */
data class DeploySummary(
    val name: String,
    val lastStatus: String,
    val updated: String,
)

/** One row of `GET /scheduler/jobs`. */
data class ScheduledSummary(
    val name: String,
    val lastStatus: String,
    val lastFire: String,
    val nextFire: String,
    val enabled: Boolean,
)

/** One subsystem of the `health` map from `/ops/status`, already classified. */
data class HealthEntry(
    val name: String,
    val status: String,
    val severity: Severity,
)

/**
 * The whole Home screen state in a single object.
 *
 * The optional fields ([deploys], [scheduled]) are null when the call failed,
 * never an empty list: empty means "there are none", null means "could not find
 * out". [fetchedAtEpochMs] uses the DEVICE clock and stamps "updated at HH:MM".
 */
data class DashboardSnapshot(
    val ops: OpsSnapshot,
    val identity: DashboardIdentity?,
    val deploys: List<DeploySummary>?,
    val scheduled: List<ScheduledSummary>?,
    val fetchedAtEpochMs: Long,
) {

    /** Resources already judged, in fixed order. Empty when the server does not expose `system`. */
    val resourceSignals: List<ResourceSignal>
        get() = ops.system?.let(::gradeResources).orEmpty()

    /** Classified subsystems, the ones off their healthy state first. */
    val health: List<HealthEntry>
        get() = ops.health.entries
            .map { (name, status) -> HealthEntry(name, status, classifyHealth(status)) }
            .sortedWith(compareByDescending<HealthEntry> { it.severity }.thenBy { it.name })

    /**
     * The health group severity: the worst component wins. `health_ok = false`
     * with every component healthy escalates to WARNING rather than showing all green.
     */
    val healthSeverity: Severity
        get() {
            val worst = health.fold(Severity.OK) { acc, entry -> worstOf(acc, entry.severity) }
            return if (!ops.healthOk) worstOf(worst, Severity.WARNING) else worst
        }

    /** Deploys that ended badly — `rolled_back` and `failed` are news, `ok` is not. */
    val brokenDeploys: List<DeploySummary>
        get() = deploys.orEmpty().filter { classifyDeploy(it.lastStatus) != Severity.OK }

    /** Scheduled jobs whose last run did not go well. */
    val brokenScheduled: List<ScheduledSummary>
        get() = scheduled.orEmpty().filter { it.enabled && classifyScheduled(it.lastStatus) != Severity.OK }

    /**
     * Everything that needs attention now, worst first: server alerts, crossed
     * resource thresholds, degraded subsystems and failed deploys/scheduled jobs.
     * The card is hidden entirely when this is empty.
     */
    val attention: List<ResourceSignal>
        get() = buildList {
            ops.alerts.forEach { add(it.toSignal()) }
            addAll(attentionSignals(resourceSignals))
            // The server contradicting itself: `health_ok = false` with every
            // component green. Report it rather than printing "all ok".
            if (!ops.healthOk && health.all { it.severity == Severity.OK }) {
                add(
                    ResourceSignal(
                        id = "health:health_ok",
                        label = "Overall health",
                        headline = "not ok",
                        detail = "the server reports health_ok = false, but no subsystem reports the problem",
                        severity = Severity.WARNING,
                        target = DashboardTarget.SERVICES,
                    ),
                )
            }
            health.filter { it.severity != Severity.OK }.forEach { entry ->
                add(
                    ResourceSignal(
                        id = "health:${entry.name}",
                        label = entry.name,
                        headline = entry.status,
                        detail = "subsystem outside its healthy state",
                        severity = entry.severity,
                        target = DashboardTarget.SERVICES,
                    ),
                )
            }
            brokenDeploys.forEach { deploy ->
                add(
                    ResourceSignal(
                        id = "deploy:${deploy.name}",
                        label = "Deploy ${deploy.name}",
                        headline = deploy.lastStatus,
                        detail = "last deploy at ${deploy.updated}",
                        severity = classifyDeploy(deploy.lastStatus),
                        target = DashboardTarget.DEPLOYS,
                    ),
                )
            }
            brokenScheduled.forEach { job ->
                add(
                    ResourceSignal(
                        id = "scheduled:${job.name}",
                        label = job.name,
                        headline = job.lastStatus,
                        detail = "last run at ${job.lastFire}",
                        severity = classifyScheduled(job.lastStatus),
                        target = DashboardTarget.SCHEDULED,
                    ),
                )
            }
            // Stable sort: within a severity, keep the assembly order (alerts,
            // resources, subsystems, deploys, scheduled jobs).
        }.sortedByDescending { it.severity }
}

/**
 * The BFF health values meaning "all good" (see `internal/mobilebff/ops_health.go`).
 * The list holds the healthy values on purpose, so any new server state shows up
 * as a deviation instead of passing as green.
 */
private val HEALTHY_STATES = setOf("ok", "connected", "running", "healthy", "active")

/** States the BFF uses for "I am trying" — bad, but not down. */
private val DEGRADED_STATES = setOf("degraded", "connecting", "reconnecting", "starting", "pending", "unknown")

internal fun classifyHealth(status: String): Severity = when (status.trim().lowercase()) {
    in HEALTHY_STATES -> Severity.OK
    in DEGRADED_STATES -> Severity.WARNING
    else -> Severity.CRITICAL
}

internal fun classifyDeploy(status: String): Severity = when (status.trim().lowercase()) {
    "ok", "success", "succeeded", "done", "deployed" -> Severity.OK
    // Rollback: the machine recovered, but the live version is not the intended
    // one, and nobody notices without looking. A warning, not critical.
    "rolled_back", "rolledback", "rollback" -> Severity.WARNING
    "failed", "error" -> Severity.CRITICAL
    // A deploy in flight is not a failure; it is movement, and the "Now" card already reports it.
    "running", "queued", "pending", "in_progress" -> Severity.OK
    else -> Severity.OK
}

internal fun classifyScheduled(status: String): Severity = when (status.trim().lowercase()) {
    "ok", "success", "succeeded", "done", "" -> Severity.OK
    else -> Severity.CRITICAL
}
