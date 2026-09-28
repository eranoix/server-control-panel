package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.OpsAlert
import dev.servercontrolpanel.data.ops.SystemSnapshot
import kotlin.math.roundToInt

enum class Severity(
    val storedName: String,
) {
    OK("OK"),
    WARNING("WARNING"),
    CRITICAL("CRITICAL"),
    ;

    companion object {
        fun fromStoredName(value: String): Severity =
            entries.firstOrNull { it.storedName == value } ?: throw IllegalArgumentException(value)
    }
}

fun worstOf(a: Severity, b: Severity): Severity = if (a >= b) a else b

enum class DashboardTarget(val sectionId: String?) {
    ALERTS("alerts.rules"),
    SERVICES("system.systemd"),
    QUEUE("queue.jobs"),
    SCHEDULED("scheduler.jobs"),
    DEPLOYS("deploy.apps"),
    METRICS("system.metrics"),
    PROCESSES("system.processes"),
    DISK("system.metrics"),
    AUDITORIA("security.audit"),
    DOCKER("docker.containers"),

    TERMINAL(null),
}

data class ResourceSignal(
    val id: String,
    val label: String,
    val headline: String,
    val detail: String,
    val severity: Severity,
    val target: DashboardTarget,
)

const val LOAD_PER_CORE_WARNING = 1.0

const val LOAD_PER_CORE_CRITICAL = 2.0

const val STEAL_WARNING_PCT = 5.0

const val STEAL_CRITICAL_PCT = 15.0

const val IOWAIT_WARNING_PCT = 10.0

const val IOWAIT_CRITICAL_PCT = 25.0

const val MEM_WARNING_PCT = 85.0
const val MEM_CRITICAL_PCT = 95.0

const val SWAP_EXHAUSTED_PCT = 95.0

const val DISK_WARNING_PCT = 85.0
const val DISK_CRITICAL_PCT = 95.0

private fun pct(value: Double): String = "${value.roundToInt()}%"

private fun grade(value: Double, warning: Double, critical: Double): Severity = when {
    value >= critical -> Severity.CRITICAL
    value >= warning -> Severity.WARNING
    else -> Severity.OK
}

fun gradeResources(system: SystemSnapshot): List<ResourceSignal> = buildList {
    add(cpuSignal(system))
    add(stealSignal(system))
    add(iowaitSignal(system))
    add(memorySignal(system))
    add(swapSignal(system))
    system.disks.forEach { add(diskSignal(it)) }
    system.net?.let { add(netSignal(it)) }
}

private fun cpuSignal(system: SystemSnapshot): ResourceSignal {
    val cpu = system.cpu
    val perCore = if (cpu.cores > 0) cpu.load1 / cpu.cores else 0.0
    val severity = if (cpu.cores > 0) {
        grade(perCore, LOAD_PER_CORE_WARNING, LOAD_PER_CORE_CRITICAL)
    } else {
        Severity.OK
    }
    val loadText = "%.2f".format(cpu.load1)
    return ResourceSignal(
        id = "cpu",
        label = "CPU",
        headline = pct(cpu.usedPercent),
        detail = when (severity) {
            Severity.OK -> "load $loadText on ${cpu.cores} cores"
            else -> "load $loadText on ${cpu.cores} cores — more runnable tasks than cores"
        },
        severity = severity,
        target = DashboardTarget.PROCESSES,
    )
}

private fun stealSignal(system: SystemSnapshot): ResourceSignal {
    val steal = system.cpu.steal
    val grave = steal >= STEAL_WARNING_PCT
    return ResourceSignal(
        id = "steal",
        label = "CPU steal",
        headline = pct(steal),
        detail = when {
            !grave -> "the hypervisor is delivering the CPU you pay for"
            else ->
                "the hypervisor is taking ${pct(steal)} of the CPU — this cannot be fixed from " +
                    "inside the VM; it is a decision to resize or move"
        },
        severity = Severity.OK,
        target = DashboardTarget.METRICS,
    )
}

private fun iowaitSignal(system: SystemSnapshot): ResourceSignal {
    val iowait = system.cpu.iowait
    val severity = grade(iowait, IOWAIT_WARNING_PCT, IOWAIT_CRITICAL_PCT)
    return ResourceSignal(
        id = "iowait",
        label = "Disk wait",
        headline = pct(iowait),
        detail = when (severity) {
            Severity.OK -> "the disk is keeping up with the CPU"
            else -> "the CPU spends ${pct(iowait)} of its time waiting for storage"
        },
        severity = severity,
        target = DashboardTarget.METRICS,
    )
}

private fun memorySignal(system: SystemSnapshot): ResourceSignal {
    val mem = system.memory
    val severity = grade(mem.usedPercent, MEM_WARNING_PCT, MEM_CRITICAL_PCT)
    return ResourceSignal(
        id = "memory",
        label = "Memory",
        headline = pct(mem.usedPercent),
        detail = "${mem.usedText} of ${mem.totalText}" + when (severity) {
            Severity.OK -> ""
            Severity.WARNING -> " — no headroom left"
            Severity.CRITICAL -> " — the OOM killer can step in at any moment"
        },
        severity = severity,
        target = DashboardTarget.PROCESSES,
    )
}

private fun swapSignal(system: SystemSnapshot): ResourceSignal {
    val swap = system.swap
    val severity = when {
        swap.usedPercent < SWAP_EXHAUSTED_PCT -> Severity.OK
        system.memory.usedPercent >= MEM_CRITICAL_PCT -> Severity.CRITICAL
        else -> Severity.WARNING
    }
    return ResourceSignal(
        id = "swap",
        label = "Swap",
        headline = pct(swap.usedPercent),
        detail = when (severity) {
            Severity.OK -> "${swap.usedText} of ${swap.totalText}"
            Severity.WARNING ->
                "${swap.usedText} of ${swap.totalText} — no room left to page; " +
                    "a memory spike goes straight to the OOM killer"
            Severity.CRITICAL ->
                "${swap.usedText} of ${swap.totalText} with RAM at its limit — " +
                    "nowhere to page to and nothing left to allocate"
        },
        severity = severity,
        target = DashboardTarget.PROCESSES,
    )
}

private fun diskSignal(disk: dev.servercontrolpanel.data.ops.DiskSnapshot): ResourceSignal {
    val severity = grade(disk.usedPercent, DISK_WARNING_PCT, DISK_CRITICAL_PCT)
    return ResourceSignal(
        id = "disco:${disk.mount}",
        label = "Disk ${disk.mount}",
        headline = pct(disk.usedPercent),
        detail = "${disk.usedText} of ${disk.totalText}" + when (severity) {
            Severity.OK -> ""
            Severity.WARNING -> " — little room left"
            Severity.CRITICAL -> " — ordinary process writes may already be failing"
        },
        severity = severity,
        target = DashboardTarget.DISK,
    )
}

private fun netSignal(net: dev.servercontrolpanel.data.ops.NetSnapshot): ResourceSignal {
    val rates = listOfNotNull(net.recvRateText?.let { "↓ $it" }, net.sentRateText?.let { "↑ $it" })
        .joinToString(" · ")
    return ResourceSignal(
        id = "network",
        label = "Network ${net.iface}",
        headline = "",
        detail = if (rates.isEmpty()) "instant rate on the uplink" else "$rates on the uplink",
        severity = Severity.OK,
        target = DashboardTarget.METRICS,
    )
}

fun attentionSignals(signals: List<ResourceSignal>): List<ResourceSignal> =
    signals.filter { it.severity != Severity.OK }.sortedByDescending { it.severity }

fun OpsAlert.toSignal(): ResourceSignal {
    val severity = when (severity.lowercase()) {
        "critical", "crit", "page" -> Severity.CRITICAL
        else -> Severity.WARNING
    }
    val unitSuffix = unit?.takeIf { it.isNotBlank() }?.let { " $it" } ?: ""
    return ResourceSignal(
        id = "alert:$name",
        label = name,
        headline = trimNumber(currentValue) + unitSuffix,
        detail = "threshold ${trimNumber(threshold)}$unitSuffix · $state",
        severity = severity,
        target = DashboardTarget.ALERTS,
    )
}

private fun trimNumber(value: Double): String =
    if (value == value.roundToInt().toDouble()) value.roundToInt().toString() else "%.1f".format(value)
