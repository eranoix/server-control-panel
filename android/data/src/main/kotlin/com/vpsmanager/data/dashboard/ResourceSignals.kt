package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.OpsAlert
import com.vpsmanager.data.ops.SystemSnapshot
import kotlin.math.roundToInt

/**
 * Severity of a dashboard signal. THE ORDER OF THE ENTRIES IS THE ORDER OF
 * URGENCY — the enum's `compareTo` is the sort criterion and the group rollup
 * ("worst wins"), so nothing here may be reordered for looks.
 */
enum class Severity(
    /** Value stored in the widget preferences; kept stable across renames. */
    val storedName: String,
) {
    OK("OK"),
    WARNING("ATENCAO"),
    CRITICAL("CRITICO"),
    ;

    companion object {
        /** Inverse of [storedName]; throws like `valueOf` for an unknown value. */
        fun fromStoredName(value: String): Severity =
            entries.firstOrNull { it.storedName == value } ?: throw IllegalArgumentException(value)
    }
}

/** The worse of two — the group rollup operation. */
fun worstOf(a: Severity, b: Severity): Severity = if (a >= b) a else b

/**
 * Where a tap leads. Home knows nothing about navigation routes (that belongs
 * to `:app`); it names the DESTINATION and whoever hosts the screen resolves
 * it. That keeps the card testable on the JVM and navigation in one place.
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
 * severity and where a tap leads.
 *
 * [detail] exists because a threshold without an explanation becomes
 * superstition — whoever reads "Swap WARNING" needs to see, on the same line,
 * WHY 100% swap deserves attention and what that means. It is what separates a
 * signal from an ornament.
 */
data class ResourceSignal(
    val id: String,
    val label: String,
    val headline: String,
    val detail: String,
    val severity: Severity,
    val target: DashboardTarget,
)

// ─────────────────────────────────────────────────────────────────────────────
// THRESHOLDS
//
// The rule that holds for all of them: an arbitrary threshold becomes a false
// alarm, and a false alarm trains the eye to ignore the colour. So every
// constant below has its reason written beside it, and the preference is
// always for a number that MEANS something (the definition of the quantity)
// over a pretty round one.
//
// The scale has two bands on purpose — WARNING ("look today") and CRITICAL
// ("look now"). Three or more bands force you to decide the difference between
// "high" and "very high" in the middle of an incident, which is when nobody
// decides well.
// ─────────────────────────────────────────────────────────────────────────────

/**
 * CPU is judged by LOAD PER CORE, not by `used_percent`.
 *
 * `used_percent` is an instantaneous sample: any compile takes it to 100% for a
 * few seconds with nothing wrong, and alerting on that is the shortest path to
 * the operator switching the alert off. Load average, on the other hand, has an
 * exact meaning: it is the average number of tasks ready to run. Divided by the
 * number of cores, it stops being a loose number and becomes a ratio
 * comparable across any machine.
 *
 * 1.0 = there are as many ready tasks as there are cores. Above that a queue
 * starts — this is not a chosen number, it is the definition of the quantity.
 */
const val LOAD_PER_CORE_WARNING = 1.0

/** 2.0 = each task waits, on average, as long as it runs. The machine is in debt. */
const val LOAD_PER_CORE_CRITICAL = 2.0

/**
 * Steal is CPU time the VM paid for and the hypervisor handed to someone else.
 * There is no fix from inside the machine — only the decision to resize or move
 * elsewhere. Precisely because it is unfixable it needs to be SEEN: without it,
 * the machine "is slow" and usage "is not high", and the operator looks in the
 * wrong place.
 *
 * Below 5% is normal noise from the host's scheduler. Past that, every latency
 * measurement taken in here is inflated by a factor that is not its own.
 */
const val STEAL_WARNING_PCT = 5.0

/** 15% = a seventh of the CPU you are paying for simply never arrives. That is a contract problem, not a code one. */
const val STEAL_CRITICAL_PCT = 15.0

/**
 * Iowait is idle CPU waiting on storage. Above 10% the bottleneck has stopped
 * being the processor and become the disk — and optimising CPU in that state is
 * work thrown away.
 */
const val IOWAIT_WARNING_PCT = 10.0

/** 25% = a quarter of CPU time is spent waiting on disk. */
const val IOWAIT_CRITICAL_PCT = 25.0

/**
 * Memory. The server computes `used` as `total - MemAvailable` (see
 * `internal/system/system.go`), so cache and buffers are ALREADY out of the
 * count — 85% here is 85% of genuinely committed memory, not Linux using free
 * RAM for cache, which is the classic misreading.
 *
 * 85% = the headroom is gone; the next large allocation will push a page into
 * swap. 95% = the OOM killer takes the field.
 */
const val MEM_WARNING_PCT = 85.0
const val MEM_CRITICAL_PCT = 95.0

/**
 * Swap is NOT measured by the same ruler as memory, and this is where most
 * dashboards lie.
 *
 * Swap is a safety net, not a resource to consume: what matters is not how much
 * has been used, but whether there is anywhere LEFT to page to when the
 * pressure comes. At 95% there is not — 5% of 8 GiB is less than the working
 * set of a single large process. The signal is "the safety net is gone", and
 * that deserves to be seen.
 *
 * But on its own it is NOT critical, and the distinction is deliberate: a
 * machine 18 days up, with swap full and 37% of RAM free, is in a normal
 * long-uptime state — the kernel paged out what nobody has touched in weeks and
 * left it at that. Painting that red every day is exactly the false alarm that
 * teaches people to ignore red. It only becomes CRITICAL when RAM is tight too
 * ([MEM_CRITICAL_PCT]), because then the combination is the recipe for an OOM:
 * nothing to allocate and nowhere to page to.
 */
const val SWAP_EXHAUSTED_PCT = 95.0

/**
 * Disk. 85% is where the remaining runway gets short enough for a log rotation
 * or a `docker pull` to finish the job, and where ext4's allocator starts to
 * fragment. 95% is where ext4's 5% root reserve runs out and writes from
 * ordinary processes begin to fail.
 *
 * The server already discards squashfs/tmpfs/overlay (`ignoredDiskFSTypes`), so
 * there is none of the false "14 mounts at 100%" that snaps produce.
 */
const val DISK_WARNING_PCT = 85.0
const val DISK_CRITICAL_PCT = 95.0

/** A percentage in short text, no decimal place — a phone dashboard read at a glance. */
private fun pct(value: Double): String = "${value.roundToInt()}%"

private fun grade(value: Double, warning: Double, critical: Double): Severity = when {
    value >= critical -> Severity.CRITICAL
    value >= warning -> Severity.WARNING
    else -> Severity.OK
}

/**
 * Judges each of the machine's resources and returns one signal per quantity,
 * ALWAYS in the same order (CPU, memory, swap, disks, network) — a stable
 * position is what lets the eye memorise where each thing lives; which ones
 * rise to the top is decided afterwards, by [attentionSignals], without
 * shuffling the card below.
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
    // cores == 0 should never happen, but dividing by it would produce
    // Infinity and a permanently CRITICAL card. With no cores declared there is
    // no ratio to compute: you do not judge what you do not know.
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
 * Stolen CPU — INFORMATION, never an alert.
 *
 * ## Why it stopped shouting
 *
 * The owner reported it, with a photo: "when stolen cpu shows up it keeps
 * popping up something needs attention. i don't want that". He is right, and
 * the reason was already written in this very file, two lines above the defect:
 * *"there is no fix from inside the VM"*.
 *
 * An alert exists to provoke an ACTION. Steal is CPU time the hypervisor handed
 * to another guest — there is no command, file or restart inside this machine
 * that changes the number. The only possible response is to resize or switch
 * providers, which is a contract decision taken once a year, not a Tuesday
 * alert.
 *
 * Alerting every day about something unfixable is the exact definition of the
 * false alarm this file's header tells you to avoid — and its cost is not the
 * annoyance: it is that red stops meaning anything. When a disk really does
 * fill up, the red card will look like yesterday's.
 *
 * ## What it still does
 *
 * The number stays VISIBLE, with the same sentence explaining it. It is the
 * answer to "why is this machine slow if usage is not high?" — the question
 * that sends you looking in the wrong place when the number is hidden. It just
 * no longer rises to the attention card, nor forces its way into the dashboard.
 *
 * It is the same treatment the network already had, and for the same reason: a
 * context number, always OK, never at the top.
 */
private fun stealSignal(system: SystemSnapshot): ResourceSignal {
    val steal = system.cpu.steal
    // The thresholds still exist and are still named: they describe TECHNICAL
    // SEVERITY, which is real. What changed is the product conclusion —
    // severity with no possible action does not become an alert.
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
        id = "memoria",
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
    // A machine with no swap configured has no signal to give: 0 of 0 is 0%,
    // and the card says so instead of faking health.
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

private fun diskSignal(disk: com.vpsmanager.data.ops.DiskSnapshot): ResourceSignal {
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

private fun netSignal(net: com.vpsmanager.data.ops.NetSnapshot): ResourceSignal {
    val rates = listOfNotNull(net.recvRateText?.let { "↓ $it" }, net.sentRateText?.let { "↑ $it" })
        .joinToString(" · ")
    return ResourceSignal(
        id = "rede",
        label = "Network ${net.iface}",
        // [headline] deliberately EMPTY: two rates with units do not fit the
        // narrow right-hand column — at phone width they pushed the label onto
        // two lines and the detail onto four. Network is the only row whose
        // value is a pair, so it goes in the detail, which has the full width.
        // The line disappears from the right instead of being squeezed.
        headline = "",
        detail = if (rates.isEmpty()) "instant rate on the uplink" else "$rates on the uplink",
        // Network is NOT judged: there is no honest threshold for "too much
        // traffic" — 200 KiB/s could be a healthy backup or an exfiltration. It
        // stays a context number, always OK, never rising to the top.
        severity = Severity.OK,
        target = DashboardTarget.METRICS,
    )
}

/**
 * The signals that DESERVE the top, worst first.
 *
 * The resources card stays where it is (fourth, as the SRE Workbook prescribes:
 * saturation is a debugging metric, not an alerting one). What rises here is
 * not "the CPU number": it is the fact that it CROSSED a threshold — at that
 * moment it stopped being saturation and became an alert, and an alert is the
 * first thing on the screen. The number stays down below, to explain why
 * afterwards.
 */
fun attentionSignals(signals: List<ResourceSignal>): List<ResourceSignal> =
    // `sortedByDescending` is STABLE: within the same severity the input order
    // survives, and the input order is [gradeResources]'s fixed one. Breaking
    // ties by id instead would sort alphabetically, which means nothing to the
    // reader.
    signals.filter { it.severity != Severity.OK }.sortedByDescending { it.severity }

/**
 * Converts an alert the SERVER raised into the same shape as the derived
 * signals, so the two live together on one card, ordered by the same ruler. A
 * server alert is never downgraded here: if the rule fired, the rule's owner
 * has already decided it matters.
 */
fun OpsAlert.toSignal(): ResourceSignal {
    val severity = when (severity.lowercase()) {
        "critical", "crit", "critico", "crítico", "page" -> Severity.CRITICAL
        else -> Severity.WARNING
    }
    val unitSuffix = unit?.takeIf { it.isNotBlank() }?.let { " $it" } ?: ""
    return ResourceSignal(
        id = "alerta:$name",
        label = name,
        headline = trimNumber(currentValue) + unitSuffix,
        detail = "threshold ${trimNumber(threshold)}$unitSuffix · $state",
        severity = severity,
        target = DashboardTarget.ALERTS,
    )
}

/** `92.0` becomes "92"; `0.5` stays "0.5". A useless zero decimal only steals width. */
private fun trimNumber(value: Double): String =
    if (value == value.roundToInt().toDouble()) value.roundToInt().toString() else "%.1f".format(value)
