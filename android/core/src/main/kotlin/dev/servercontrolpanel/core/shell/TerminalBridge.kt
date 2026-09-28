package dev.servercontrolpanel.core.shell

object TerminalBridge {

    @Volatile
    private var pending: TerminalCommand? = null

    @Volatile
    var onRequestTerminal: (() -> Unit)? = null

    fun send(command: String, origin: String): Boolean {
        pending = TerminalCommand(command = command, origin = origin)
        val open = onRequestTerminal ?: return false
        open()
        return true
    }

    fun consume(): TerminalCommand? {
        val current = pending
        pending = null
        return current
    }

    fun hasPending(): Boolean = pending != null

    fun discard() {
        pending = null
    }
}

data class TerminalCommand(
    val command: String,
    val origin: String,
)

object BridgeCommands {

    private const val LOG_LINES = 200

    fun viewFile(path: String) = TerminalCommand(
        command = "cat ${shellQuoted(path)} ",
        origin = "file ${shortName(path)}",
    )

    fun listDir(path: String) = TerminalCommand(
        command = "ls -la ${shellQuoted(path)} ",
        origin = "folder ${shortName(path)}",
    )

    fun containerLogs(name: String) = TerminalCommand(
        command = "docker logs -f --tail $LOG_LINES ${shellQuoted(name)} ",
        origin = "container $name",
    )

    fun unitJournal(unit: String) = TerminalCommand(
        command = "journalctl -u ${shellQuoted(unit)} -n $LOG_LINES -f ",
        origin = "service $unit",
    )

    fun diskUsage(mountPoint: String) = TerminalCommand(
        command = "du -xh --max-depth=1 ${shellQuoted(mountPoint)} | sort -rh | head -20 ",
        origin = "disk $mountPoint",
    )

    fun heaviestProcesses() = TerminalCommand(
        command = "ps -eo pid,pcpu,pmem,rss,comm --sort=-pcpu | head -20 ",
        origin = "processes",
    )

    private fun shortName(path: String): String =
        path.trimEnd('/').substringAfterLast('/').ifBlank { path }
}
