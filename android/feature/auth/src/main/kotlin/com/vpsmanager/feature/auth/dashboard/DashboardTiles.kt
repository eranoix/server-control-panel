package com.vpsmanager.feature.auth.dashboard

import com.vpsmanager.data.dashboard.DashboardSnapshot
import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.data.dashboard.Severity

/**
 * A panel block: a number, what it is, and what it means.
 *
 * [sub] carries the context that turns the number into information — `78%`
 * decides nothing; `78% · 87 GB free` decides. It is the same rule as the
 * `detail` on the resource signals, and it exists for the same reason: a value
 * with no scale becomes superstition.
 *
 * [wide] is the only variation in size, and it is deliberately binary. Blocks
 * with three free-form sizes produce a grid the person starts tidying instead
 * of reading; what really needs the width is the block whose sentence does not
 * fit in half a screen (a rolled-back deploy, a loss of contact).
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
 * Everything that CAN become a block, out of what the server sent.
 *
 * ## Why derived, and never a fixed list
 *
 * The catalogue is born from the snapshot: if the server starts exposing a new
 * disk, it shows up here with no app release — the same rule that lets
 * Administration show a new screen with no release. A fixed list of blocks on
 * the client would be the divergence that killed `/m/`, on a smaller scale.
 *
 * ## The order
 *
 * The judged resources come first and in the fixed order of `gradeResources`
 * (CPU, memory, swap, disks, network), because a stable position is what lets
 * the eye memorise where each thing sits. The operational aggregates come
 * after. Which one climbs to the top of the GRID is decided in
 * [visibleTiles], without shuffling the catalogue.
 */
fun tileCatalog(snapshot: DashboardSnapshot): List<DashboardTile> = buildList {
    snapshot.resourceSignals.forEach { signal ->
        add(
            DashboardTile(
                id = signal.id,
                label = signal.label,
                // The network is the only signal with no headline (two
                // rates do not fit the column) — the block shows the detail as
                // the value instead of a blank space where a number should be.
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
                // Naming the worst subsystem rather than saying "1 with a
                // problem": the name is what decides where to go, and it is
                // already here.
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
 * The blocks the grid REALLY shows.
 *
 * ## The rule that is not up for negotiation: the panel cannot hide a fire
 *
 * The person picks their blocks, and that choice rules — over everything but
 * one thing. A block in a **CRITICAL** state appears even if they never picked
 * it, and it appears **first**.
 *
 * Without that rule, a panel assembled on a good day becomes a false promise:
 * the person picked CPU, memory and queue, the disk filled up, and the screen
 * stays green because the disk was not on the list. A panel that can omit the
 * one thing that is wrong is worse than no panel at all, because it is
 * consulted with confidence.
 *
 * WARNING does not force its way in — only CRITICAL does. The difference is
 * the same as between the two threshold bands: "look today" fits inside the
 * person's choice; "look now" does not.
 *
 * ## The order
 *
 * Critical ones first (in catalogue order, which is stable), then the chosen
 * ones in the order they were chosen. A critical one that was ALSO chosen
 * appears only once, at the top.
 */
fun visibleTiles(catalog: List<DashboardTile>, chosen: List<String>): List<DashboardTile> {
    val byId = catalog.associateBy { it.id }
    val critical = catalog.filter { it.severity == Severity.CRITICAL }
    val criticalIds = critical.mapTo(mutableSetOf()) { it.id }
    val remaining = chosen.mapNotNull(byId::get).filterNot { it.id in criticalIds }
    return critical + remaining
}

/**
 * The blocks a person gets before choosing anything.
 *
 * It is neither "the panel starts empty" nor "the panel starts with
 * everything": empty forces you to assemble it before seeing any value, and
 * everything hands over 15 blocks nobody asked for. These four are the
 * questions every operator asks — how much machine is left, and is anything
 * running.
 */
// "cpu" is OUT of the default (the owner asked for it). On this server that
// block shows the CPU STOLEN by the hypervisor — a number you do not control
// and cannot act on, taking up the top of the first screen every day. It was
// exactly the "bar" the owner ordered removed.
//
// The block IS STILL in the catalogue: anyone who wants to track the steal
// adds it in two taps. What changes is that it is no longer imposed on people
// who never asked for it.
val INITIAL_TILES: List<String> = listOf("memoria", TILE_HEALTH, TILE_QUEUE)

const val TILE_QUEUE = "fila"
const val TILE_HEALTH = "saude"
const val TILE_DEPLOYS = "deploys"
const val TILE_SCHEDULED = "agendados"
const val TILE_UPTIME = "uptime"
