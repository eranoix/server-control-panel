package com.vpsmanager.core.shell

/**
 * Lets any screen send a command to the terminal (a file becomes `cat`, a
 * container `docker logs`, a unit `journalctl`).
 *
 * The command is never executed: it is inserted with a trailing space and the
 * user presses Enter, as with [shellInsertionText]. A tap must never run
 * something the operator has not read, and the command stays editable.
 *
 * Only one command is pending at a time; a second request replaces the first
 * (last one wins) instead of gluing two commands on one line.
 *
 * Lives in `:core` so feature senders and the terminal consumer do not depend on each other.
 */
object TerminalBridge {

    @Volatile
    private var pending: TerminalCommand? = null

    /**
     * Opens the terminal. Registered by `:app`, which owns the routes, so no
     * feature needs to know a route or thread a callback through its composables.
     */
    @Volatile
    var onRequestTerminal: (() -> Unit)? = null

    /**
     * Queues [command] and requests the terminal.
     *
     * Returns `false` when [onRequestTerminal] is not registered; the caller must
     * then tell the user. The command stays pending anyway, so it is not lost if
     * the terminal is opened some other way.
     */
    fun send(command: String, origin: String): Boolean {
        pending = TerminalCommand(command = command, origin = origin)
        val open = onRequestTerminal ?: return false
        open()
        return true
    }

    /**
     * Takes and clears the pending command, so it cannot reappear the next time
     * the terminal opens.
     */
    fun consume(): TerminalCommand? {
        val current = pending
        pending = null
        return current
    }

    /** Whether a command is waiting, without consuming it. */
    fun hasPending(): Boolean = pending != null

    /** Drops the pending command, e.g. when the user cancels before the terminal opens. */
    fun discard() {
        pending = null
    }
}

/**
 * A command to insert into the terminal line. [origin] is shown alongside it so
 * the user later knows where an untyped command came from.
 */
data class TerminalCommand(
    val command: String,
    val origin: String,
)

/**
 * The commands the bridge can build, kept in one place so screens do not diverge.
 *
 * Every server-provided name goes through [shellQuoted]: the server is not a
 * trusted source for a shell line, and a `;` in a name would execute.
 */
object BridgeCommands {

    /** How many log lines to bring back before following live. */
    private const val LOG_LINES = 200

    /** View a file. `cat`, not `less`, which is interactive and holds the session. */
    fun viewFile(path: String) = TerminalCommand(
        command = "cat ${shellQuoted(path)} ",
        origin = "file ${shortName(path)}",
    )

    /** List a directory in detail. */
    fun listDir(path: String) = TerminalCommand(
        command = "ls -la ${shellQuoted(path)} ",
        origin = "folder ${shortName(path)}",
    )

    /** A container's logs, followed live (`-f`); Ctrl-C on the key bar stops it. */
    fun containerLogs(name: String) = TerminalCommand(
        command = "docker logs -f --tail $LOG_LINES ${shellQuoted(name)} ",
        origin = "container $name",
    )

    /** A systemd unit's journal, live. */
    fun unitJournal(unit: String) = TerminalCommand(
        command = "journalctl -u ${shellQuoted(unit)} -n $LOG_LINES -f ",
        origin = "service $unit",
    )

    /** What is filling a disk. `-x` keeps `du` on one filesystem, out of `/proc` and `/sys`. */
    fun diskUsage(mountPoint: String) = TerminalCommand(
        command = "du -xh --max-depth=1 ${shellQuoted(mountPoint)} | sort -rh | head -20 ",
        origin = "disk $mountPoint",
    )

    /** The processes using the most CPU right now. */
    fun heaviestProcesses() = TerminalCommand(
        command = "ps -eo pid,pcpu,pmem,rss,comm --sort=-pcpu | head -20 ",
        origin = "processes",
    )

    /** Last path segment, so the origin label stays short. */
    private fun shortName(path: String): String =
        path.trimEnd('/').substringAfterLast('/').ifBlank { path }
}
