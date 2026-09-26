package com.vpsmanager.data.ops

import com.vpsmanager.mobileapiclient.model.CPUMetrics
import com.vpsmanager.mobileapiclient.model.DiskMetrics
import com.vpsmanager.mobileapiclient.model.MemoryMetrics
import com.vpsmanager.mobileapiclient.model.NetMetrics
import com.vpsmanager.mobileapiclient.model.SystemMetrics

/**
 * The machine's resources, as `GET /api/mobile/v1/ops/status` now returns them
 * in the `system` field.
 *
 * Every quantity arrives as a PAIR: the raw number (to compare against a
 * threshold) and the text the server has already formatted (to display). The
 * rule is not to reformat here — "8.0 GiB" is the server's to write, and two
 * diverging formattings of the same number is how a dashboard starts lying on
 * its own.
 */
data class SystemSnapshot(
    val cpu: CpuSnapshot,
    val memory: MemorySnapshot,
    val swap: MemorySnapshot,
    val disks: List<DiskSnapshot>,
    val net: NetSnapshot?,
    val serverTime: String,
    val serverTimeEpoch: Long,
    val uptimeText: String,
    val hostname: String?,
    val platform: String?,
)

/**
 * CPU.
 *
 * [steal] and [iowait] are here, and not hidden away, on purpose: they are the
 * two quantities that explain "the machine is slow and usage is not high".
 * Steal is time the hypervisor took — from inside the VM there is nothing to
 * fix, only something to know: the capacity that was paid for is not arriving.
 */
data class CpuSnapshot(
    val usedPercent: Double,
    val cores: Long,
    val load1: Double,
    val load5: Double,
    val load15: Double,
    val steal: Double,
    val iowait: Double,
    val model: String?,
)

/** Memory or swap — the server returns both with the same shape. */
data class MemorySnapshot(
    val usedPercent: Double,
    val usedText: String,
    val totalText: String,
    val freeBytes: Long,
)

/** A mount point. The server already filters out the pseudo filesystems. */
data class DiskSnapshot(
    val mount: String,
    val usedPercent: Double,
    val usedText: String,
    val totalText: String,
    val fstype: String?,
)

/** The uplink interface and the instantaneous rate on it. */
data class NetSnapshot(
    val iface: String,
    val sentRateText: String?,
    val recvRateText: String?,
)

internal fun SystemMetrics.toSnapshot() = SystemSnapshot(
    cpu = cpu.toSnapshot(),
    memory = memory.toSnapshot(),
    swap = swap.toSnapshot(),
    disks = (disks ?: emptyList()).map(DiskMetrics::toSnapshot),
    net = net?.toSnapshot(),
    serverTime = serverTime,
    serverTimeEpoch = serverTimeEpoch,
    uptimeText = uptimeText,
    hostname = hostname,
    platform = platform,
)

private fun CPUMetrics.toSnapshot() = CpuSnapshot(
    usedPercent = usedPercent,
    cores = cores,
    load1 = load1,
    load5 = load5,
    load15 = load15,
    steal = steal,
    iowait = iowait,
    model = model,
)

private fun MemoryMetrics.toSnapshot() = MemorySnapshot(
    usedPercent = usedPercent,
    usedText = usedText,
    totalText = totalText,
    freeBytes = free,
)

private fun DiskMetrics.toSnapshot() = DiskSnapshot(
    mount = mount,
    usedPercent = usedPercent,
    usedText = usedText,
    totalText = totalText,
    fstype = fstype,
)

private fun NetMetrics.toSnapshot() = NetSnapshot(
    iface = `interface`,
    sentRateText = sentRateText,
    recvRateText = recvRateText,
)
