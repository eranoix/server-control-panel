package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.OpsAlert
import dev.servercontrolpanel.data.ops.SystemSnapshot
import kotlin.math.roundToInt

/**
 * Severity of a dashboard signal. Entry order is urgency order: `compareTo` drives
 * sorting and the "worst wins" rollup, so never reorder the entries.
 */
enum class Severity(
    /** Value stored in the widget preferences; kept stable across renames. */
    val storedName: String,
) {
    OK("OK"),
    WARNING("WARNING"),
    CRITICAL("CRITICAL"),
    ;

    companion object {
        /** Inverse of [storedName]; throws like `valueOf` for an unknown value. */
        fun fromStoredName(value: String): Severity =
            entries.firstOrNull { it.storedName == value } ?: throw IllegalArgumentException(value)
    }
}

/** The worse of two severities (the group rollup operation). */
fun worstOf(a: Severity, b: Severity): Severity = if (a >= b) a else b

/**
 * Where a tap leads. Home names the destination and the host resolves it to a
 * route (routes belong to `:app`), which keeps the card testable on the JVM.
 */
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

    /** Not an SDUI section: it is the shell's own Terminal destination. */
    TERMINAL(null),
}

/**
 * A reading already judged: the number, the sentence that explains it, the
 * severity and where a tap leads. [detail] says why the value matters, so a
 * threshold is never shown without its reason.
 */
data class ResourceSignal(
    val id: String,
    val label: String,
    val headline: String,
    val detail: String,
    val severity: Severity,
    val target: DashboardTarget,
)

// Thresholds: every constant carries its reason, since an arbitrary threshold
// becomes a false alarm that trains people to ignore the colour. Two bands only:
// WARNING ("look today") and CRITICAL ("look now").

/**
 * CPU is judged by load per core, not `used_percent`, which spikes to 100% on any
 * compile. 1.0 means as many runnable tasks as cores; above that a queue forms.
 */
const val LOAD_PER_CORE_WARNING = 1.0

/** 2.0 = each task waits, on average, as long as it runs. */
const val LOAD_PER_CORE_CRITICAL = 2.0

/**
 * Steal: CPU time the hypervisor gave to another guest. Nothing inside the VM
 * fixes it, but it explains "slow with low usage". Below 5% is normal host noise.
 */
const val STEAL_WARNING_PCT = 5.0

/** 15% = a seventh of the CPU you pay for never arrives. */
const val STEAL_CRITICAL_PCT = 15.0

/** Iowait above 10%: the bottleneck is the disk, not the processor. */
const val IOWAIT_WARNING_PCT = 10.0

/** 25% = a quarter of CPU time is spent waiting on disk. */
const val IOWAIT_CRITICAL_PCT = 25.0

/**
 * Memory. The server computes `used` as `total - MemAvailable`
 * (`internal/system/system.go`), so cache and buffers are already excluded.
 * 85% = no headroom left; 95% = the OOM killer is close.
 */
const val MEM_WARNING_PCT = 85.0
const val MEM_CRITICAL_PCT = 95.0

/**
 * Swap is a safety net, so what matters is whether there is room left to page,
 * not how much is used. Full swap alone is a WARNING (normal after long uptime);
 * it is CRITICAL only when RAM is also above [MEM_CRITICAL_PCT], since then an
 * OOM is imminent.
 */
const val SWAP_EXHAUSTED_PCT = 95.0

/**
 * Disk. At 85% a log rotation or `docker pull` can fill it and ext4 starts to
 * fragment; at 95% ext4's 5% root reserve runs out and normal writes fail. The
 * server already drops squashfs/tmpfs/overlay mounts (`ignoredDiskFSTypes`).
 */
const val DISK_WARNING_PCT = 85.0
const val DISK_CRITICAL_PCT = 95.0

/** A percentage as short text with no decimals, for reading at a glance. */
private fun pct(value: Double): String = "${value.roundToInt()}%"

private fun grade(value: Double, warning: Double, critical: Double): Severity = when {
    value >= critical -> Severity.CRITICAL
    value >= warning -> Severity.WARNING
    else -> Severity.OK
}

/**
 * Judges each resource and returns one signal per quantity, always in the same
 * order (CPU, memory, swap, disks, network) so positions stay stable; which ones
 * rise to the top is decided by [attentionSignals].
 */
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
    // With zero cores there is no ratio to compute; avoid a division producing
    // Infinity and a permanently CRITICAL card.
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

/**
 * Stolen CPU is shown as information, never as an alert: nothing inside the VM
 * can change it (only resizing or moving can), and a daily alert nobody can act
 * on teaches people to ignore red. The number stays visible to explain slowness.
 */
private fun stealSignal(system: SystemSnapshot): ResourceSignal {
    val steal = system.cpu.steal
    // The threshold still describes technical severity, but without a possible
    // action it does not become an alert.
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
    // No swap configured shows 0 of 0 as 0%, which is honest rather than healthy.
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
        // Headline left empty: two rates with units do not fit the narrow right
        // column on a phone, so they go in the full-width detail.
        headline = "",
        detail = if (rates.isEmpty()) "instant rate on the uplink" else "$rates on the uplink",
        // Network is not judged: there is no honest threshold for "too much traffic".
        severity = Severity.OK,
        target = DashboardTarget.METRICS,
    )
}

/**
 * The signals that deserve the top, worst first. The resources card keeps its
 * place; a resource rises only once it crosses a threshold and becomes an alert.
 */
fun attentionSignals(signals: List<ResourceSignal>): List<ResourceSignal> =
    // `sortedByDescending` is stable, so ties keep [gradeResources]'s fixed order.
    signals.filter { it.severity != Severity.OK }.sortedByDescending { it.severity }

/**
 * Converts a server-raised alert into the same shape as derived signals so both
 * share one card and ordering. A server alert is never downgraded here.
 */
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

/** `92.0` becomes "92"; `0.5` stays "0.5". */
private fun trimNumber(value: Double): String =
    if (value == value.roundToInt().toDouble()) value.roundToInt().toString() else "%.1f".format(value)
