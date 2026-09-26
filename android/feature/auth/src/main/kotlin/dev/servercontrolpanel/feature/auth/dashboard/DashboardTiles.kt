package dev.servercontrolpanel.feature.auth.dashboard

import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.DashboardTarget
import dev.servercontrolpanel.data.dashboard.Severity

/**
 * A dashboard tile: a value, its label and its severity.
 *
 * [sub] gives the value context (e.g. `87 GB free`). [wide] is the only size
 * variation, for tiles whose sentence does not fit half the width.
 */
data class DashboardTile(
    val id: String,
    val label: String,
    val value: String,
    val sub: String,
    val severity: Severity,
    val target: DashboardTarget,
    val wide: Boolean = false,
)

/**
 * Every possible tile, derived from the server snapshot so new resources (e.g. a
 * new disk) appear without an app release.
 *
 * Resource tiles come first in the stable `gradeResources` order, then the
 * operational aggregates. [visibleTiles] decides what goes on top.
 */
fun tileCatalog(snapshot: DashboardSnapshot): List<DashboardTile> = buildList {
    snapshot.resourceSignals.forEach { signal ->
        add(
            DashboardTile(
                id = signal.id,
                label = signal.label,
                // Network has no headline (two rates), so its detail becomes the value.
                value = signal.headline.ifBlank { signal.detail },
                sub = if (signal.headline.isBlank()) "" else signal.detail,
                severity = signal.severity,
                target = signal.target,
            ),
        )
    }

    add(
        DashboardTile(
            id = TILE_QUEUE,
            label = "Queue",
            value = (snapshot.ops.queueQueued + snapshot.ops.queueRunning).toString(),
            sub = when {
                snapshot.ops.queueRunning > 0 -> "${snapshot.ops.queueRunning} running"
                snapshot.ops.queueQueued > 0 -> "waiting"
                else -> "idle"
            },
            severity = Severity.OK,
            target = DashboardTarget.QUEUE,
        ),
    )

    val health = snapshot.health
    add(
        DashboardTile(
            id = TILE_HEALTH,
            label = "Health",
            value = health.size.toString(),
            sub = when (snapshot.healthSeverity) {
                Severity.OK -> "all ok"
                // Name the failing subsystem; that is what tells the user where to go.
                else -> health.firstOrNull()?.let { "${it.name}: ${it.status}" } ?: "degraded"
            },
            severity = snapshot.healthSeverity,
            target = DashboardTarget.SERVICES,
            wide = snapshot.healthSeverity != Severity.OK,
        ),
    )

    val broken = snapshot.brokenDeploys
    snapshot.deploys?.let { deploys ->
        add(
            DashboardTile(
                id = TILE_DEPLOYS,
                label = "Deploys",
                value = if (broken.isEmpty()) deploys.size.toString() else broken.size.toString(),
                sub = broken.firstOrNull()?.let { "${it.name}: ${it.lastStatus}" }
                    ?: "none failing",
                severity = if (broken.isEmpty()) Severity.OK else Severity.CRITICAL,
                target = DashboardTarget.DEPLOYS,
                wide = broken.isNotEmpty(),
            ),
        )
    }

    val badScheduled = snapshot.brokenScheduled
    snapshot.scheduled?.let { scheduled ->
        add(
            DashboardTile(
                id = TILE_SCHEDULED,
                label = "Scheduled",
                value = scheduled.count { it.enabled }.toString(),
                sub = badScheduled.firstOrNull()?.let { "${it.name}: ${it.lastStatus}" }
                    ?: "active",
                severity = if (badScheduled.isEmpty()) Severity.OK else Severity.WARNING,
                target = DashboardTarget.SCHEDULED,
            ),
        )
    }

    snapshot.ops.system?.let { system ->
        add(
            DashboardTile(
                id = TILE_UPTIME,
                label = "Uptime",
                value = system.uptimeText,
                sub = "no restarts",
                severity = Severity.OK,
                target = DashboardTarget.METRICS,
            ),
        )
    }
}

/**
 * The tiles the grid actually shows.
 *
 * Invariant: the dashboard must never hide a fire. Any CRITICAL tile is shown
 * first even if not chosen (WARNING is not forced in). Then come the chosen
 * tiles in the user's order; a critical tile that was also chosen appears once.
 */
fun visibleTiles(catalog: List<DashboardTile>, chosen: List<String>): List<DashboardTile> {
    val byId = catalog.associateBy { it.id }
    val critical = catalog.filter { it.severity == Severity.CRITICAL }
    val criticalIds = critical.mapTo(mutableSetOf()) { it.id }
    val remaining = chosen.mapNotNull(byId::get).filterNot { it.id in criticalIds }
    return critical + remaining
}

/**
 * Default tiles before the user chooses any. "cpu" is left out on purpose: here
 * it shows hypervisor steal, which the user cannot act on; it stays in the catalog.
 */
val INITIAL_TILES: List<String> = listOf("memory", TILE_HEALTH, TILE_QUEUE)

const val TILE_QUEUE = "queue"
const val TILE_HEALTH = "health"
const val TILE_DEPLOYS = "deploys"
const val TILE_SCHEDULED = "scheduled"
const val TILE_UPTIME = "uptime"
