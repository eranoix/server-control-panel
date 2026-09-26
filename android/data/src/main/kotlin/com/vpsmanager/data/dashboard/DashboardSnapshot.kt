package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.OpsSnapshot

/** Who is signed in, for the identity footer. */
data class DashboardIdentity(
    val user: String,
    val email: String,
    val isAdmin: Boolean,
)

/** Uma linha de `GET /deploy/apps`. */
data class DeploySummary(
    val name: String,
    val lastStatus: String,
    val updated: String,
)

/** Uma linha de `GET /scheduler/jobs`. */
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
 * The optional fields ([deploys], [scheduled]) are NULL when the call failed —
 * never an empty list. The difference matters: an empty list means "there are
 * no deploys", null means "I could not find out", and the dashboard says one or
 * the other instead of showing zero, which would be a lie.
 *
 * [fetchedAtEpochMs] comes from the DEVICE clock, and it is what stamps
 * "updated at HH:MM". Without that stamp, "nothing changed" and "the fetch
 * hung" look identical on screen.
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

    /** Subsistemas classificados, os que desviaram primeiro. */
    val health: List<HealthEntry>
        get() = ops.health.entries
            .map { (name, status) -> HealthEntry(name, status, classifyHealth(status)) }
            .sortedWith(compareByDescending<HealthEntry> { it.severity }.thenBy { it.name })

    /**
     * The severity of the health group — "the worst component wins", which is
     * the standard status-page rollup.
     *
     * `health_ok = false` with every component looking healthy is the server
     * contradicting itself, and the dashboard would rather believe the worse of
     * the two: it escalates to WARNING instead of showing all green.
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
     * Everything that needs attention NOW, worst first: the alerts the server
     * raised, the resource thresholds that were crossed, the degraded
     * subsystems and the deploys/scheduled jobs that ended badly.
     *
     * It is card number one on the screen, and the only one that disappears
     * entirely when empty — silence here is the good news, and taking up space
     * to say "nothing" would push down what matters.
     */
    val attention: List<ResourceSignal>
        get() = buildList {
            ops.alerts.forEach { add(it.toSignal()) }
            addAll(attentionSignals(resourceSignals))
            // The server contradicting itself: `health_ok = false` with every
            // component green. Without this line the dashboard would print
            // "9 subsystems · all ok" while the server itself says it is not
            // ok — exactly the kind of lie this card exists in order not to
            // tell.
            if (!ops.healthOk && health.all { it.severity == Severity.OK }) {
                add(
                    ResourceSignal(
                        id = "saude:health_ok",
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
                        id = "saude:${entry.name}",
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
                        id = "agendado:${job.name}",
                        label = job.name,
                        headline = job.lastStatus,
                        detail = "last run at ${job.lastFire}",
                        severity = classifyScheduled(job.lastStatus),
                        target = DashboardTarget.SCHEDULED,
                    ),
                )
            }
            // STABLE sort: within the same severity the order in which the list
            // was assembled survives — server alerts, resource thresholds,
            // subsystems, deploys, scheduled jobs. It is an order that means
            // something; breaking ties by id would sort alphabetically.
        }.sortedByDescending { it.severity }
}

/**
 * The BFF health vocabulary. `ok` and `connected` are the two values
 * `internal/mobilebff/ops_health.go` emits for "all good"; any other word is a
 * deviation.
 *
 * The list holds the HEALTHY values, not the bad ones, and that is deliberate:
 * a new state invented on the server tomorrow shows up as a deviation (visible,
 * investigable) instead of passing as green by omission.
 */
private val HEALTHY_STATES = setOf("ok", "connected", "running", "healthy", "ativo")

/** States the BFF uses for "I am trying" — bad, but not down. */
private val DEGRADED_STATES = setOf("degraded", "connecting", "reconnecting", "starting", "pending", "unknown")

internal fun classifyHealth(status: String): Severity = when (status.trim().lowercase()) {
    in HEALTHY_STATES -> Severity.OK
    in DEGRADED_STATES -> Severity.WARNING
    else -> Severity.CRITICAL
}

internal fun classifyDeploy(status: String): Severity = when (status.trim().lowercase()) {
    "ok", "success", "succeeded", "done", "deployed" -> Severity.OK
    // Rollback is the interesting case: the machine saved itself, so it is not
    // an ongoing incident — but it does mean the version that is live is NOT
    // the one meant to go live, and nobody finds that out without looking.
    // A warning, not a critical.
    "rolled_back", "rolledback", "rollback" -> Severity.WARNING
    "failed", "error", "erro" -> Severity.CRITICAL
    // A deploy in flight is not a failure; it is movement, and the "Now" card already reports it.
    "running", "queued", "pending", "in_progress" -> Severity.OK
    else -> Severity.OK
}

internal fun classifyScheduled(status: String): Severity = when (status.trim().lowercase()) {
    "ok", "success", "succeeded", "done", "" -> Severity.OK
    else -> Severity.CRITICAL
}
