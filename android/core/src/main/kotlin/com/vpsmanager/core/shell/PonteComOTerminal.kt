package com.vpsmanager.core.shell

/**
 * The bridge: any screen can say "send this to the terminal".
 *
 * ## Why this is the app's most valuable feature
 *
 * Every server dashboard on the market has the same ceiling: it shows what it
 * anticipated showing. When the question leaves the anticipated set — *why did
 * this container restart at 3am?* — the dashboard ends and the person goes to
 * SSH. This app has the terminal INSIDE it, so the bridge is the one thing
 * here no competitor can copy without rebuilding the terminal first.
 *
 * A file becomes `cat`. A container becomes `docker logs`. A unit becomes
 * `journalctl`. The screen does not stop being useful when the question gets
 * specific: it hands the question to the place that answers any of them.
 *
 * ## The rule that is not negotiable: NEVER run it by itself
 *
 * The command is INSERTED into the line, with a trailing space, and the Enter
 * belongs to the person. It is the same decision already taken in
 * [textoDeInsercaoParaShell] for attachments, and for the same reason: a `\n`
 * would execute on the spot, and no tap on "view the logs" may fire something
 * the operator has not read. Beyond that, inserting without running leaves the
 * command EDITABLE — changing `--tail 200` to `--tail 2000` before running is
 * half of the real work.
 *
 * ## State, and why there is only one
 *
 * There is only ONE pending command. Two would be a queue nobody asked for:
 * the person taps "view logs", the terminal opens, the command goes in. If
 * they tap again before the terminal opens, the right answer is last-one-wins
 * — that is what they just asked for — and not to stack two commands that
 * would appear glued together on the same line.
 *
 * The object lives in `:core` (pure Kotlin, no Android) because the SENDERS
 * are the features and the CONSUMER is the terminal: putting the bridge on
 * either side would create a dependency between features that does not exist
 * today.
 */
object PonteComOTerminal {

    @Volatile
    private var pendente: ComandoParaOTerminal? = null

    /**
     * How the shell opens the Terminal. Registered by whoever knows the routes
     * (`:app`), never by the features.
     *
     * The alternative would be every screen receiving an `aoAbrirTerminal`
     * callback and passing it down three levels of composition to the menu item
     * that uses it. That would exist purely to transport, and transporting is
     * precisely what a bridge does. What still holds is what matters: **no
     * feature knows a route** — it asks for "the terminal", and the shell
     * decides what that means.
     */
    @Volatile
    var aoPedirOTerminal: (() -> Unit)? = null

    /**
     * Queues [comando] and asks for the terminal.
     *
     * Returns `false` when nobody has registered [aoPedirOTerminal] — and the
     * caller has to SAY so on screen. A tap that does nothing and explains
     * nothing is worse than a disabled button: it teaches the person not to
     * trust the button.
     *
     * The command stays pending anyway, on purpose: if the shell opens the
     * terminal by another route shortly afterwards, the request is not lost.
     */
    fun mandar(comando: String, origem: String): Boolean {
        pendente = ComandoParaOTerminal(comando = comando, origem = origem)
        val abrir = aoPedirOTerminal ?: return false
        abrir()
        return true
    }

    /**
     * Takes the pending command, if there is one. Taking is destructive by
     * design: a command that stayed pending would reappear the NEXT time the
     * person opened the terminal, without their having asked for anything.
     */
    fun consumir(): ComandoParaOTerminal? {
        val atual = pendente
        pendente = null
        return atual
    }

    /** Whether something is waiting. A read with no effect — for the screen to decide what to show. */
    fun temPendente(): Boolean = pendente != null

    /** Discards without consuming. Used when the person gives up before the terminal opens. */
    fun descartar() {
        pendente = null
    }
}

/**
 * A command ready to go into the line, with the sentence saying where it came
 * from.
 *
 * [origem] is not decoration: the command appears in a session that may have
 * ten lines of something else above it. Without saying "came from: nginx
 * (container)", whoever looks at the screen two minutes later finds a
 * `docker logs` they do not remember typing.
 */
data class ComandoParaOTerminal(
    val comando: String,
    val origem: String,
)

/**
 * The commands the bridge knows how to build.
 *
 * They live here, rather than scattered across the screens, for a maintenance
 * reason that has already cost this project dearly: when the same idea is
 * written in five places, it diverges at five speeds. Here there is one place
 * to fix `--tail` or swap `journalctl -u` for something else.
 *
 * All of them go through [comAspasParaShell]: container names, file paths and
 * unit names come from the SERVER, and the server is not a trusted source for
 * assembling a shell line — a name containing `;` would be execution, not
 * confusion.
 */
object ComandosDaPonte {

    /** How many log lines to bring back before following live. */
    private const val LINHAS_DE_LOG = 200

    /** View a file. `cat` and not `less`: `less` is interactive and holds the session. */
    fun verArquivo(caminho: String) = ComandoParaOTerminal(
        comando = "cat ${comAspasParaShell(caminho)} ",
        origem = "file ${nomeCurto(caminho)}",
    )

    /** List a directory in detail — the equivalent of "open this in the shell". */
    fun listarPasta(caminho: String) = ComandoParaOTerminal(
        comando = "ls -la ${comAspasParaShell(caminho)} ",
        origem = "folder ${nomeCurto(caminho)}",
    )

    /**
     * A container's logs, live. `-f` on purpose: whoever asks for container
     * logs almost always wants to see what comes NEXT, and leaving `-f` is a
     * Ctrl-C that the key bar already has.
     */
    fun logsDoContainer(nome: String) = ComandoParaOTerminal(
        comando = "docker logs -f --tail $LINHAS_DE_LOG ${comAspasParaShell(nome)} ",
        origem = "container $nome",
    )

    /** A systemd unit's journal, live. */
    fun journalDaUnidade(unidade: String) = ComandoParaOTerminal(
        comando = "journalctl -u ${comAspasParaShell(unidade)} -n $LINHAS_DE_LOG -f ",
        origem = "service $unidade",
    )

    /**
     * What is filling a disk that has run out of space.
     *
     * `du` with `-x` (does not cross filesystems) and sorted: without the `-x`,
     * a full `/` sends the command down into `/proc` and `/sys` and it takes
     * minutes to answer the wrong question.
     */
    fun ocupacaoDoDisco(ponto: String) = ComandoParaOTerminal(
        comando = "du -xh --max-depth=1 ${comAspasParaShell(ponto)} | sort -rh | head -20 ",
        origem = "disk $ponto",
    )

    /** The processes weighing most right now — the question that follows "the CPU is high". */
    fun processosMaisPesados() = ComandoParaOTerminal(
        comando = "ps -eo pid,pcpu,pmem,rss,comm --sort=-pcpu | head -20 ",
        origem = "processes",
    )

    /** The file or folder name, so the origin sentence does not become a whole path. */
    private fun nomeCurto(caminho: String): String =
        caminho.trimEnd('/').substringAfterLast('/').ifBlank { caminho }
}
