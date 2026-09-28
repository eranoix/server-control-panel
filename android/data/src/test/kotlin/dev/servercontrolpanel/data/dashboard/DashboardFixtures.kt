package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.CpuSnapshot
import dev.servercontrolpanel.data.ops.DiskSnapshot
import dev.servercontrolpanel.data.ops.MemorySnapshot
import dev.servercontrolpanel.data.ops.NetSnapshot
import dev.servercontrolpanel.data.ops.OpsSnapshot
import dev.servercontrolpanel.data.ops.SystemSnapshot

internal fun productionLike(
    swapUsedPercent: Double = 99.99814033419625,
    memUsedPercent: Double = 63.13659490136221,
    steal: Double = 7.0427350429260835,
    load1: Double = 11.97,
    iowait: Double = 0.0,
    rootUsedPercent: Double = 73.26499030846479,
) = SystemSnapshot(
    cpu = CpuSnapshot(
        usedPercent = 91.25341143536828,
        cores = 8,
        load1 = load1,
        load5 = 8.24,
        load15 = 6.37,
        steal = steal,
        iowait = iowait,
        model = "AMD EPYC 9355P 32-Core Processor",
    ),
    memory = MemorySnapshot(
        usedPercent = memUsedPercent,
        usedText = "19.8 GiB",
        totalText = "31.3 GiB",
        freeBytes = 11_410_059_264,
    ),
    swap = MemorySnapshot(
        usedPercent = swapUsedPercent,
        usedText = "8.0 GiB",
        totalText = "8.0 GiB",
        freeBytes = 159_744,
    ),
    disks = listOf(
        DiskSnapshot("/", rootUsedPercent, "283.1 GiB", "386.4 GiB", "ext4"),
        DiskSnapshot("/boot", 14.234868653326846, "116.5 MiB", "880.4 MiB", "ext4"),
        DiskSnapshot("/boot/efi", 5.849866378362187, "6.1 MiB", "104.3 MiB", "vfat"),
    ),
    net = NetSnapshot(iface = "eth0", sentRateText = "256.3 KiB/s", recvRateText = "147.5 KiB/s"),
    serverTime = "2026-09-06T07:08:22Z",
    serverTimeEpoch = 1_788_678_502,
    uptimeText = "18d 13h 19m",
    hostname = "host01",
    platform = "ubuntu 24.04",
)

internal fun opsReal(system: SystemSnapshot? = productionLike()) = OpsSnapshot(
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

internal fun realisticDeploys() = listOf(
    DeploySummary(name = "hello", lastStatus = "rolled_back", updated = "2026-07-19 13:17 UTC"),
)

internal fun realisticScheduled() = listOf(
    ScheduledSummary("Terminal session backup every 10min", "ok", "2026-09-06 07:00 UTC", "2026-09-06 07:10 UTC", true),
    ScheduledSummary("Preview env reaper", "ok", "2026-09-06 06:17 UTC", "2026-09-06 07:17 UTC", true),
    ScheduledSummary("Keep-alive 5h — Jordan", "ok", "2026-09-06 06:23 UTC", "2026-09-06 07:23 UTC", true),
    ScheduledSummary("Keep-alive 5h — Sam", "ok", "2026-09-06 06:38 UTC", "2026-09-06 07:38 UTC", true),
    ScheduledSummary("Full backup, daily 04:30 UTC", "ok", "2026-09-06 04:30 UTC", "2026-09-07 04:30 UTC", true),
)

internal fun snapshotReal(
    ops: OpsSnapshot = opsReal(),
    deploys: List<DeploySummary>? = realisticDeploys(),
    scheduled: List<ScheduledSummary>? = realisticScheduled(),
    fetchedAtEpochMs: Long = 1_788_678_502_000,
) = DashboardSnapshot(
    ops = ops,
    identity = DashboardIdentity(user = "tester", email = "test@northwind.example", isAdmin = true),
    deploys = deploys,
    scheduled = scheduled,
    fetchedAtEpochMs = fetchedAtEpochMs,
)
