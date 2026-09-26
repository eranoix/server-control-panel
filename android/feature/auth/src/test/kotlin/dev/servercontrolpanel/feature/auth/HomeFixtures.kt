package dev.servercontrolpanel.feature.auth

import dev.servercontrolpanel.data.dashboard.DashboardIdentity
import dev.servercontrolpanel.data.dashboard.DashboardResult
import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.DashboardSource
import dev.servercontrolpanel.data.dashboard.DeploySummary
import dev.servercontrolpanel.data.dashboard.ScheduledSummary
import dev.servercontrolpanel.data.ops.CpuSnapshot
import dev.servercontrolpanel.data.ops.DiskSnapshot
import dev.servercontrolpanel.data.ops.MemorySnapshot
import dev.servercontrolpanel.data.ops.NetSnapshot
import dev.servercontrolpanel.data.ops.OpsSnapshot
import dev.servercontrolpanel.data.ops.SystemSnapshot

/**
 * A real `/ops/status` response: swap at 99.998%, CPU at 91% with load 11.97 on
 * 8 cores and 7% steal, yet `alerts` empty and `health_ok = true`. A naive
 * dashboard would show this as all fine.
 */
internal fun systemReal(
    swapUsedPercent: Double = 99.99814033419625,
    memUsedPercent: Double = 63.13659490136221,
    steal: Double = 7.0427350429260835,
    load1: Double = 11.97,
    rootUsedPercent: Double = 73.26499030846479,
) = SystemSnapshot(
    cpu = CpuSnapshot(
        usedPercent = 91.25341143536828,
        cores = 8,
        load1 = load1,
        load5 = 8.24,
        load15 = 6.37,
        steal = steal,
        iowait = 0.0,
        model = "AMD EPYC 9355P 32-Core Processor",
    ),
    memory = MemorySnapshot(memUsedPercent, "19.8 GiB", "31.3 GiB", 11_410_059_264),
    swap = MemorySnapshot(swapUsedPercent, "8.0 GiB", "8.0 GiB", 159_744),
    disks = listOf(
        DiskSnapshot("/", rootUsedPercent, "283.1 GiB", "386.4 GiB", "ext4"),
        DiskSnapshot("/boot", 14.23, "116.5 MiB", "880.4 MiB", "ext4"),
    ),
    net = NetSnapshot("eth0", "256.3 KiB/s", "147.5 KiB/s"),
    serverTime = "2026-09-06T07:08:22Z",
    serverTimeEpoch = 1_788_678_502,
    uptimeText = "18d 13h 19m",
    hostname = "host01",
    platform = "ubuntu 24.04",
)

internal fun opsReal(system: SystemSnapshot? = systemReal()) = OpsSnapshot(
    health = mapOf(
        "audit" to "ok",
        "model_router" to "ok",
        "config" to "ok",
        "docker" to "ok",
        "dtach" to "ok",
        "secrets" to "ok",
        "videocall" to "ok",
        "whatsapp" to "connected",
    ),
    healthOk = true,
    queueRunning = 0,
    queueQueued = 0,
    alerts = emptyList(),
    system = system,
)

internal fun snapshotReal(
    ops: OpsSnapshot = opsReal(),
    deploys: List<DeploySummary>? = listOf(DeploySummary("hello", "rolled_back", "2026-07-19 13:17 UTC")),
    scheduled: List<ScheduledSummary>? = listOf(
        ScheduledSummary("Terminal session backup", "ok", "2026-09-06 07:00 UTC", "2026-09-06 07:10 UTC", true),
    ),
    identity: DashboardIdentity? = DashboardIdentity("tester", "test@northwind.example", isAdmin = true),
    fetchedAtEpochMs: Long = 1_788_678_502_000,
) = DashboardSnapshot(
    ops = ops,
    identity = identity,
    deploys = deploys,
    scheduled = scheduled,
    fetchedAtEpochMs = fetchedAtEpochMs,
)

/** A machine where nothing crossed a threshold and nothing fired. */
internal fun calmSnapshot() = snapshotReal(
    ops = opsReal(systemReal(swapUsedPercent = 12.0, steal = 0.0, load1 = 1.2, rootUsedPercent = 30.0)),
    deploys = listOf(DeploySummary("hello", "ok", "2026-07-19 13:17 UTC")),
)

internal class FakeDashboardSource(
    private val onLoad: suspend () -> DashboardResult,
) : DashboardSource {
    override suspend fun load(): DashboardResult = onLoad()
}
