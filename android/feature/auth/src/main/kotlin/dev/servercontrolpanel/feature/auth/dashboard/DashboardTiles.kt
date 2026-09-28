package dev.servercontrolpanel.feature.auth.dashboard

import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.DashboardTarget
import dev.servercontrolpanel.data.dashboard.Severity

data class DashboardTile(
    val id: String,
    val label: String,
    val value: String,
    val sub: String,
    val severity: Severity,
    val target: DashboardTarget,
    val wide: Boolean = false,
)

fun tileCatalog(snapshot: DashboardSnapshot): List<DashboardTile> = buildList {
    snapshot.resourceSignals.forEach { signal ->
        add(
            DashboardTile(
                id = signal.id,
                label = signal.label,
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

fun visibleTiles(catalog: List<DashboardTile>, chosen: List<String>): List<DashboardTile> {
    val byId = catalog.associateBy { it.id }
    val critical = catalog.filter { it.severity == Severity.CRITICAL }
    val criticalIds = critical.mapTo(mutableSetOf()) { it.id }
    val remaining = chosen.mapNotNull(byId::get).filterNot { it.id in criticalIds }
    return critical + remaining
}

val INITIAL_TILES: List<String> = listOf("memory", TILE_HEALTH, TILE_QUEUE)

const val TILE_QUEUE = "queue"
const val TILE_HEALTH = "health"
const val TILE_DEPLOYS = "deploys"
const val TILE_SCHEDULED = "scheduled"
const val TILE_UPTIME = "uptime"
