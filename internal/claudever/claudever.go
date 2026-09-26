// Package claudever answers a question the Claude Code CLI raises and does not
// settle: "which of my sessions are running an old version?".
//
// The CLI downloads a new version on its own (~1 to 2 a day) and writes
// "✓ Update installed · Restart to update" in its status bar. The notice stays
// there until the session is restarted — and anyone working in sessions that last
// several days sees it permanently, without knowing WHICH sessions are behind nor
// when they can restart them without losing anything.
//
// Here being behind becomes a verifiable fact: the running version is read from
// the binary the process has open (/proc/<pid>/exe), not from a state file that
// may be lying.
package claudever

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Processo is a running `claude` and the version it actually loaded.
type Processo struct {
	PID     int    `json:"pid"`
	Versao  string `json:"versao"`
	Sessao  string `json:"sessao,omitempty"` // owning session, when it can be known
	Atual   bool   `json:"atual"`            // already on the installed version?
	Diretor string `json:"cwd,omitempty"`    // helps tell which is which
	// Alvo says HOW to restart this process when it does not belong to a panel
	// session. "" = an ordinary session (type into the pane); "recovery" = the
	// Claude in the recovery container, which restarts through the container.
	Alvo string `json:"alvo,omitempty"`
	// Ref is the version installed IN ITS OWN ENVIRONMENT, when that environment
	// is not the host's. Only filled for external processes — see LevantarExterno.
	Ref string `json:"ref,omitempty"`
}

// Estado is the complete answer: what is installed and who has not taken it yet.
type Estado struct {
	Instalada  string     `json:"instalada"`
	Processos  []Processo `json:"processos"`
	Defasados  int        `json:"defasados"`
	Disponivel bool       `json:"disponivel"` // were we able to determine the installed version?
}

// raizProc is injectable for tests; in production it is /proc.
var raizProc = "/proc"

// caminhoCLI is the symlink that points at the installed version.
var caminhoCLI = "/root/.local/bin/claude"

// VersaoInstalada reads where the CLI's symlink points. The target file's name IS
// the version (…/claude/versions/2.1.240) — that is how the official installer
// organizes it, and reading the link avoids running the binary just to ask.
func VersaoInstalada() string {
	alvo, err := filepath.EvalSymlinks(caminhoCLI)
	if err != nil {
		return ""
	}
	return versaoDoCaminho(alvo)
}

// versaoDoCaminho extracts "2.1.240" from ".../claude/versions/2.1.240".
func versaoDoCaminho(p string) string {
	if p == "" || !strings.Contains(p, "/claude/versions/") {
		return ""
	}
	base := filepath.Base(p)
	// A version has digits and dots; anything else is an odd path, and returning
	// empty beats inventing a value.
	for _, r := range base {
		if (r < '0' || r > '9') && r != '.' {
			return ""
		}
	}
	return base
}

// Levantar sweeps the processes and returns the state. It never fails: on a
// screen that exists to inform, an error reading /proc counts as "don't know",
// not as a visible error.
func Levantar(donoDaSessao func(pid int) string) Estado {
	e := Estado{Instalada: VersaoInstalada(), Processos: []Processo{}}
	e.Disponivel = e.Instalada != ""

	ents, err := os.ReadDir(raizProc)
	if err != nil {
		return e
	}
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		alvo, err := os.Readlink(filepath.Join(raizProc, ent.Name(), "exe"))
		if err != nil {
			continue // a process of another user, or one already dead
		}
		versao := versaoDoCaminho(alvo)
		if versao == "" {
			continue
		}
		// A process from ANOTHER container does not count: it has its own Claude
		// installation, at the same path inside its own namespace. The recovery
		// container runs with --pid=host, so its processes show up here — and the
		// first version of this code listed it as "behind" when it was in fact
		// NEWER than the host.
		if !mesmoMount(pid) {
			continue
		}
		p := Processo{PID: pid, Versao: versao, Atual: !ehMaisVelha(versao, e.Instalada)}
		if cwd, err := os.Readlink(filepath.Join(raizProc, ent.Name(), "cwd")); err == nil {
			p.Diretor = cwd
		}
		if donoDaSessao != nil {
			p.Sessao = donoDaSessao(pid)
		}
		if !p.Atual {
			e.Defasados++
		}
		e.Processos = append(e.Processos, p)
	}
	return e
}

// ehMaisVelha compares two "a.b.c" versions and says whether `v` is BEHIND `ref`.
//
// Comparing by equality (the first version of this) marked as behind anything
// that was AHEAD — the recovery container, which has its own newer installation,
// showed up on the "needs restart" list. Only what is behind actually needs a
// restart.
func ehMaisVelha(v, ref string) bool {
	if v == "" || ref == "" || v == ref {
		return false
	}
	pv, pr := strings.Split(v, "."), strings.Split(ref, ".")
	for i := 0; i < len(pv) || i < len(pr); i++ {
		a, b := 0, 0
		if i < len(pv) {
			a, _ = strconv.Atoi(pv[i])
		}
		if i < len(pr) {
			b, _ = strconv.Atoi(pr[i])
		}
		if a != b {
			return a < b
		}
	}
	return false
}

// mesmoMount says whether the process shares this process's mount namespace, that
// is, whether it sees the SAME filesystem — and therefore the same Claude
// installation. Without this check, processes from containers that run with
// --pid=host enter the tally carrying their own installation.
//
// When it cannot read (permissions, a dying process), it assumes the host's: the
// cost of listing one extra process is a line on screen; the cost of hiding a
// genuinely outdated session is the operator believing everything is up to date.
func mesmoMount(pid int) bool {
	meu, err := os.Readlink(filepath.Join(raizProc, "self", "ns", "mnt"))
	if err != nil {
		return true
	}
	dele, err := os.Readlink(filepath.Join(raizProc, strconv.Itoa(pid), "ns", "mnt"))
	if err != nil {
		return true
	}
	return meu == dele
}

// PaiDe reads the PPID from /proc/<pid>/stat.
//
// Field 4 of stat is the PPID, but field 2 is the executable's name IN
// PARENTHESES and may contain spaces — splitting the whole line on spaces gets it
// wrong in those cases. Hence the cut is made after the last ')'.
func PaiDe(pid int) int {
	b, err := os.ReadFile(filepath.Join(raizProc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return 0
	}
	campos := strings.Fields(s[i+2:]) // [0]=state, [1]=ppid
	if len(campos) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(campos[1])
	if err != nil {
		return 0
	}
	return ppid
}

// AncestralEm climbs the process tree from pid and returns the first ancestor
// present in `alvos`, or 0. The hop ceiling avoids an infinite loop if /proc
// returns something inconsistent (which has happened with a recycled PID).
func AncestralEm(pid int, alvos map[int]bool) int {
	for salto := 0; salto < 32 && pid > 1; salto++ {
		if alvos[pid] {
			return pid
		}
		pai := PaiDe(pid)
		if pai == pid || pai <= 0 {
			return 0
		}
		pid = pai
	}
	return 0
}

// AncestralPorArgv climbs the tree from pid and returns the value in `marcas`
// whose KEY appears in the /proc/<pid>/cmdline of some ancestor (or of pid
// itself). "" when none matches.
//
// It exists because AncestralEm depends on knowing the session's PID, and the
// dtach backend NEVER records a PID — the master is forked by `dtach -n` under
// `systemd-run --scope`, so the PID the server sees when spawning dies right
// afterwards and is useless as an anchor. Result: with dtach active, EVERY process
// ended up session-less and the version panel's "Restart" button was born disabled
// on every row — useless by construction, not for want of a session.
//
// What really anchors is the master's argv, which carries the socket path
// (`dtach -n /…/session-sox/<name>.sock …`) — unique per session and stable for as
// long as it lives. It matches by plain substring: the socket path is specific
// enough not to collide, and the hop ceiling inherits the same reason as
// AncestralEm (a recycled PID has already produced a loop here).
func AncestralPorArgv(pid int, marcas map[string]string) string {
	if len(marcas) == 0 {
		return ""
	}
	for salto := 0; salto < 32 && pid > 1; salto++ {
		b, err := os.ReadFile(filepath.Join(raizProc, strconv.Itoa(pid), "cmdline"))
		if err == nil && len(b) > 0 {
			// cmdline is NUL-separated; it becomes spaces only so Contains does
			// not fail on an argument glued to its neighbor.
			linha := strings.ReplaceAll(string(b), "\x00", " ")
			for marca, valor := range marcas {
				if marca != "" && strings.Contains(linha, marca) {
					return valor
				}
			}
		}
		pai := PaiDe(pid)
		if pai == pid || pai <= 0 {
			return ""
		}
		pid = pai
	}
	return ""
}

// LevantarExterno sweeps the `claude` processes running in ANOTHER mount
// namespace — in practice, containers — that carry `marcadorEnv` in their environ.
//
// It exists because Levantar discards those processes on purpose (mesmoMount), and
// the reason for discarding them still holds: the container has its OWN CLI
// installation, so comparing it against the host's version is apples to oranges —
// that is exactly how the first version of that code called a Claude "behind" when
// it was NEWER than the host. Here the reference is ITS OWN installation: `Ref`
// comes from the CLI symlink inside its namespace, reachable from the host through
// /proc/<pid>/root without paying a `docker exec` every time the panel opens.
//
// `alvo` travels to the front end to say HOW to restart (see Processo.Alvo).
func LevantarExterno(marcadorEnv, alvo string) []Processo {
	fora := []Processo{}
	if marcadorEnv == "" {
		return fora
	}
	ents, err := os.ReadDir(raizProc)
	if err != nil {
		return fora
	}
	marca := []byte(marcadorEnv)
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		// Deliberate order, cheapest to costliest: one readlink on `exe` knocks out
		// almost every process on the machine before environ is ever touched.
		exe, err := os.Readlink(filepath.Join(raizProc, ent.Name(), "exe"))
		if err != nil {
			continue
		}
		versao := versaoDoCaminho(exe)
		if versao == "" || mesmoMount(pid) {
			continue
		}
		env, err := os.ReadFile(filepath.Join(raizProc, ent.Name(), "environ"))
		if err != nil {
			continue
		}
		achou := false
		for _, kv := range bytesSplitNUL(env) {
			if string(kv) == string(marca) {
				achou = true
				break
			}
		}
		if !achou {
			continue
		}
		p := Processo{PID: pid, Versao: versao, Alvo: alvo, Atual: true}
		// The CLI's symlink INSIDE its own namespace, seen from the host.
		if ref, err := os.Readlink(filepath.Join(raizProc, ent.Name(), "root", "root", ".local", "bin", "claude")); err == nil {
			p.Ref = versaoDoCaminho(resolveRel(filepath.Join(raizProc, ent.Name(), "root"), ref))
		}
		if p.Ref != "" {
			p.Atual = !ehMaisVelha(versao, p.Ref)
		}
		if cwd, err := os.Readlink(filepath.Join(raizProc, ent.Name(), "cwd")); err == nil {
			p.Diretor = cwd
		}
		fora = append(fora, p)
	}
	return fora
}

// resolveRel re-anchors, at the process's root, the destination of a symlink read
// from inside /proc/<pid>/root: the target is absolute in ITS namespace, and
// without re-anchoring it points at the HOST's same-named path.
//
// Do NOT swap the os.Readlink above for filepath.EvalSymlinks (nor for
// `readlink -f` in a script): FULL resolution follows the absolute target all the
// way to the host's root and returns the host's version, silently. Measured in the
// recovery container, which had only 2.1.241 and 2.1.243 installed:
//
//	docker exec … readlink /root/.local/bin/claude   → …/versions/2.1.243  (truth)
//	readlink -f /proc/<pid>/root/root/.local/bin/claude → …/versions/2.1.246  (host!)
//
// 2.1.246 did not even exist inside the container. A full resolver here would make
// the panel compare the container against itself using the host's version as the
// reference — back to the very error mesmoMount was written to eliminate.
func resolveRel(raiz, alvo string) string {
	if strings.HasPrefix(alvo, "/") {
		return filepath.Join(raiz, alvo)
	}
	return alvo
}

// bytesSplitNUL slices the environ (KEY=VALUE pairs separated by NUL).
func bytesSplitNUL(b []byte) [][]byte {
	var out [][]byte
	inicio := 0
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			if i > inicio {
				out = append(out, b[inicio:i])
			}
			inicio = i + 1
		}
	}
	if inicio < len(b) {
		out = append(out, b[inicio:])
	}
	return out
}
