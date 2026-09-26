package com.vpsmanager.feature.auth

import com.vpsmanager.data.dashboard.DashboardIdentity
import com.vpsmanager.data.dashboard.DashboardResult
import com.vpsmanager.data.dashboard.DashboardSnapshot
import com.vpsmanager.data.dashboard.DashboardSource
import com.vpsmanager.data.dashboard.DeploySummary
import com.vpsmanager.data.dashboard.ScheduledSummary
import com.vpsmanager.data.ops.CpuSnapshot
import com.vpsmanager.data.ops.DiskSnapshot
import com.vpsmanager.data.ops.MemorySnapshot
import com.vpsmanager.data.ops.NetSnapshot
import com.vpsmanager.data.ops.OpsSnapshot
import com.vpsmanager.data.ops.SystemSnapshot

/**
 * A REAL machine, exactly as `/ops/status` returned it: swap at 99.998%, CPU
 * at 91% with load 11.97 on 8 cores, 7% steal — and `alerts` empty with
 * `health_ok = true`.
 *
 * It is the main fixture because it is the case a naive dashboard paints as
 * "all fine".
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
        "claude_router" to "ok",
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
        ScheduledSummary("Backup de sessões do terminal", "ok", "2026-09-06 07:00 UTC", "2026-09-06 07:10 UTC", true),
    ),
    identity: DashboardIdentity? = DashboardIdentity("teste", "test@northwind.example", isAdmin = true),
    fetchedAtEpochMs: Long = 1_788_678_502_000,
) = DashboardSnapshot(
    ops = ops,
    identity = identity,
    deploys = deploys,
    scheduled = scheduled,
    fetchedAtEpochMs = fetchedAtEpochMs,
)

/** A machine where nothing crossed a threshold and nothing fired. */
internal fun snapshotCalmo() = snapshotReal(
    ops = opsReal(systemReal(swapUsedPercent = 12.0, steal = 0.0, load1 = 1.2, rootUsedPercent = 30.0)),
    deploys = listOf(DeploySummary("hello", "ok", "2026-07-19 13:17 UTC")),
)

internal class FakeDashboardSource(
    private val onLoad: suspend () -> DashboardResult,
) : DashboardSource {
    override suspend fun load(): DashboardResult = onLoad()
}
