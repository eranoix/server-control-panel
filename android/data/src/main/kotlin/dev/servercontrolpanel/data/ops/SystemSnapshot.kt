package dev.servercontrolpanel.data.ops

import dev.servercontrolpanel.mobileapiclient.model.CPUMetrics
import dev.servercontrolpanel.mobileapiclient.model.DiskMetrics
import dev.servercontrolpanel.mobileapiclient.model.MemoryMetrics
import dev.servercontrolpanel.mobileapiclient.model.NetMetrics
import dev.servercontrolpanel.mobileapiclient.model.SystemMetrics

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

data class MemorySnapshot(
    val usedPercent: Double,
    val usedText: String,
    val totalText: String,
    val freeBytes: Long,
)

data class DiskSnapshot(
    val mount: String,
    val usedPercent: Double,
    val usedText: String,
    val totalText: String,
    val fstype: String?,
)

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
