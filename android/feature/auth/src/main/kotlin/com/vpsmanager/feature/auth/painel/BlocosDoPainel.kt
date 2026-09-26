package com.vpsmanager.feature.auth.painel

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
 * [largo] is the only variation in size, and it is deliberately binary. Blocks
 * with three free-form sizes produce a grid the person starts tidying instead
 * of reading; what really needs the width is the block whose sentence does not
 * fit in half a screen (a rolled-back deploy, a loss of contact).
 */
data class BlocoDoPainel(
    val id: String,
    val rotulo: String,
    val valor: String,
    val sub: String,
    val severidade: Severity,
    val alvo: DashboardTarget,
    val largo: Boolean = false,
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
 * [blocosVisiveis], without shuffling the catalogue.
 */
fun catalogoDeBlocos(snapshot: DashboardSnapshot): List<BlocoDoPainel> = buildList {
    snapshot.resourceSignals.forEach { sinal ->
        add(
            BlocoDoPainel(
                id = sinal.id,
                rotulo = sinal.label,
                // The network is the only signal with no headline (two
                // rates do not fit the column) — the block shows the detail as
                // the value instead of a blank space where a number should be.
                valor = sinal.headline.ifBlank { sinal.detail },
                sub = if (sinal.headline.isBlank()) "" else sinal.detail,
                severidade = sinal.severity,
                alvo = sinal.target,
            ),
        )
    }

    add(
        BlocoDoPainel(
            id = BLOCO_FILA,
            rotulo = "Queue",
            valor = (snapshot.ops.queueQueued + snapshot.ops.queueRunning).toString(),
            sub = when {
                snapshot.ops.queueRunning > 0 -> "${snapshot.ops.queueRunning} running"
                snapshot.ops.queueQueued > 0 -> "waiting"
                else -> "idle"
            },
            severidade = Severity.OK,
            alvo = DashboardTarget.FILA,
        ),
    )

    val saude = snapshot.health
    add(
        BlocoDoPainel(
            id = BLOCO_SAUDE,
            rotulo = "Health",
            valor = saude.size.toString(),
            sub = when (snapshot.healthSeverity) {
                Severity.OK -> "all ok"
                // Naming the worst subsystem rather than saying "1 with a
                // problem": the name is what decides where to go, and it is
                // already here.
                else -> saude.firstOrNull()?.let { "${it.name}: ${it.status}" } ?: "degraded"
            },
            severidade = snapshot.healthSeverity,
            alvo = DashboardTarget.SERVICOS,
            largo = snapshot.healthSeverity != Severity.OK,
        ),
    )

    val quebrados = snapshot.brokenDeploys
    snapshot.deploys?.let { deploys ->
        add(
            BlocoDoPainel(
                id = BLOCO_DEPLOYS,
                rotulo = "Deploys",
                valor = if (quebrados.isEmpty()) deploys.size.toString() else quebrados.size.toString(),
                sub = quebrados.firstOrNull()?.let { "${it.name}: ${it.lastStatus}" }
                    ?: "none failing",
                severidade = if (quebrados.isEmpty()) Severity.OK else Severity.CRITICO,
                alvo = DashboardTarget.DEPLOYS,
                largo = quebrados.isNotEmpty(),
            ),
        )
    }

    val agendadosRuins = snapshot.brokenScheduled
    snapshot.scheduled?.let { agendados ->
        add(
            BlocoDoPainel(
                id = BLOCO_AGENDADOS,
                rotulo = "Scheduled",
                valor = agendados.count { it.enabled }.toString(),
                sub = agendadosRuins.firstOrNull()?.let { "${it.name}: ${it.lastStatus}" }
                    ?: "active",
                severidade = if (agendadosRuins.isEmpty()) Severity.OK else Severity.ATENCAO,
                alvo = DashboardTarget.AGENDADOS,
            ),
        )
    }

    snapshot.ops.system?.let { sistema ->
        add(
            BlocoDoPainel(
                id = BLOCO_UPTIME,
                rotulo = "Uptime",
                valor = sistema.uptimeText,
                sub = "no restarts",
                severidade = Severity.OK,
                alvo = DashboardTarget.METRICAS,
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
fun blocosVisiveis(catalogo: List<BlocoDoPainel>, escolhidos: List<String>): List<BlocoDoPainel> {
    val porId = catalogo.associateBy { it.id }
    val criticos = catalogo.filter { it.severidade == Severity.CRITICO }
    val idsCriticos = criticos.mapTo(mutableSetOf()) { it.id }
    val restantes = escolhidos.mapNotNull(porId::get).filterNot { it.id in idsCriticos }
    return criticos + restantes
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
val BLOCOS_INICIAIS: List<String> = listOf("memoria", BLOCO_SAUDE, BLOCO_FILA)

const val BLOCO_FILA = "fila"
const val BLOCO_SAUDE = "saude"
const val BLOCO_DEPLOYS = "deploys"
const val BLOCO_AGENDADOS = "agendados"
const val BLOCO_UPTIME = "uptime"
