package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.OpsSnapshot

data class DashboardIdentity(
    val user: String,
    val email: String,
    val isAdmin: Boolean,
)

data class DeploySummary(
    val name: String,
    val lastStatus: String,
    val updated: String,
)

data class ScheduledSummary(
    val name: String,
    val lastStatus: String,
    val lastFire: String,
    val nextFire: String,
    val enabled: Boolean,
)

data class HealthEntry(
    val name: String,
    val status: String,
    val severity: Severity,
)

data class DashboardSnapshot(
    val ops: OpsSnapshot,
    val identity: DashboardIdentity?,
    val deploys: List<DeploySummary>?,
    val scheduled: List<ScheduledSummary>?,
    val fetchedAtEpochMs: Long,
) {

    val resourceSignals: List<ResourceSignal>
        get() = ops.system?.let(::gradeResources).orEmpty()

    val health: List<HealthEntry>
        get() = ops.health.entries
            .map { (name, status) -> HealthEntry(name, status, classifyHealth(status)) }
            .sortedWith(compareByDescending<HealthEntry> { it.severity }.thenBy { it.name })

    val healthSeverity: Severity
        get() {
            val worst = health.fold(Severity.OK) { acc, entry -> worstOf(acc, entry.severity) }
            return if (!ops.healthOk) worstOf(worst, Severity.WARNING) else worst
        }

    val brokenDeploys: List<DeploySummary>
        get() = deploys.orEmpty().filter { classifyDeploy(it.lastStatus) != Severity.OK }

    val brokenScheduled: List<ScheduledSummary>
        get() = scheduled.orEmpty().filter { it.enabled && classifyScheduled(it.lastStatus) != Severity.OK }

    val attention: List<ResourceSignal>
        get() = buildList {
            ops.alerts.forEach { add(it.toSignal()) }
            addAll(attentionSignals(resourceSignals))
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
        }.sortedByDescending { it.severity }
}

private val HEALTHY_STATES = setOf("ok", "connected", "running", "healthy", "active")

private val DEGRADED_STATES = setOf("degraded", "connecting", "reconnecting", "starting", "pending", "unknown")

internal fun classifyHealth(status: String): Severity = when (status.trim().lowercase()) {
    in HEALTHY_STATES -> Severity.OK
    in DEGRADED_STATES -> Severity.WARNING
    else -> Severity.CRITICAL
}

internal fun classifyDeploy(status: String): Severity = when (status.trim().lowercase()) {
    "ok", "success", "succeeded", "done", "deployed" -> Severity.OK
    "rolled_back", "rolledback", "rollback" -> Severity.WARNING
    "failed", "error" -> Severity.CRITICAL
    "running", "queued", "pending", "in_progress" -> Severity.OK
    else -> Severity.OK
}

internal fun classifyScheduled(status: String): Severity = when (status.trim().lowercase()) {
    "ok", "success", "succeeded", "done", "" -> Severity.OK
    else -> Severity.CRITICAL
}
