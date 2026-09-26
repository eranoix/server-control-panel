package pty

// gravador.go — THE SESSION LOG MUST NOT HAVE HOLES.
//
// ## The defect
//
// The tee that writes the log lives INSIDE the connection: it is the PTY→WS pump
// that, in passing, writes to the file. With nobody attached there is no pump,
// and the session stays alive producing output that is recorded nowhere. `dtach`
// keeps no screen and the program keeps nothing — that output simply stops
// existing.
//
// Measured: five markers emitted with the client disconnected, five lost, and
// reattaching recovers none of them. In practice it is the whole interval in
// which a person closes the laptop and moves to another computer — exactly the
// stretch of history they come back wanting to read.
//
// A "primer" on the client (fetch the log and replay it on attach) does not fix
// this: it shows what is recorded, and what happened with nobody watching was
// never recorded at all. Only a recorder that never detaches solves it.
//
// ## The shape
//
// For every live session the server keeps ONE `dtach -a` client of its own,
// whose only job is to read the PTY and write to the log. It does not draw, does
// not answer, does not write to anyone's terminal.
//
// Three consequences worth more than they look:
//
//  1. The log becomes continuous. The hole is gone.
//
//  2. The log gets ONE stable writer. The write lease
//     (`sessionlog_compartilhado.go`) exists because connections came and went
//     and the scribe's post changed hands — and every handover cost up to one
//     lease window of hole. The recorder arrives first, never leaves, and never
//     loses the post.
//
//  3. `dtach`'s chatter disappears from the log. "Erase the screen" and
//     "[detached]" are messages addressed to the client that ATTACHES or LEAVES;
//     the recorder does neither once it is born, so it never receives them. The
//     filter stays there as a safety net, not as a patch.
//
// ## The recorder's size, which is where this could have spoiled everything
//
// The `dtach` master looks at the pty sizes of the attached clients. A recorder
// with a size of its own would drag the whole session to it. That is why it
// registers an APPLIER (it receives the session's effective size and puts it on
// its own pty) but does NOT enter the minimum calculation: it obeys, it never
// has an opinion. While nobody has attached, its pty stays as it was born — 0x0,
// which `dtach` ignores.

import (
	"log"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// gravador is the `dtach -a` client that only records.
type gravador struct {
	cmd  *exec.Cmd
	ptmx *os.File
	// This session's server screen. It lives here because what asks for it is the
	// history handler, and the recorder is the sole owner of its lifecycle — see
	// [telaDe].
	tela  *telaDaSessao
	fecha sync.Once
}

func (g *gravador) para() {
	g.fecha.Do(func() {
		if g.ptmx != nil {
			_ = g.ptmx.Close()
		}
		if g.cmd != nil && g.cmd.Process != nil {
			_ = g.cmd.Process.Kill()
			_ = g.cmd.Wait()
		}
	})
}

var (
	gravadoresMu sync.Mutex
	// key = the session's log path (it identifies the session AND the owner).
	gravadores = map[string]*gravador{}
)

// GaranteGravador starts the session's recorder, if the session exists and does
// not already have one. Idempotent and best-effort: failing here costs the hole
// in the log that already existed before, never someone's terminal.
//
// It NEVER creates a session. The backend's `Attach` creates the master when it
// does not exist, and a recorder that resurrected a dead session would be a
// creative way of never letting anything end — hence the `Has` check first.
func GaranteGravador(dataDir, user, name string, reg *Registry) {
	if dataDir == "" || user == "" || name == "" {
		return
	}
	chave := sessionLogPath(dataDir, user, name)

	gravadoresMu.Lock()
	if _, jaTem := gravadores[chave]; jaTem {
		gravadoresMu.Unlock()
		return
	}
	// Reserve the key before leaving the lock: two connections arriving together
	// must not open two recorders for the same session (that would be two `dtach`
	// clients and two writes of the same byte).
	gravadores[chave] = nil
	gravadoresMu.Unlock()

	g, err := abreGravador(dataDir, user, name, reg, chave)
	gravadoresMu.Lock()
	if err != nil {
		delete(gravadores, chave)
	} else {
		gravadores[chave] = g
	}
	gravadoresMu.Unlock()
}

func abreGravador(dataDir, user, name string, reg *Registry, chave string) (*gravador, error) {
	backend := NewSessionBackend(dataDir, reg)
	if viva, err := backend.Has(name); err != nil || !viva {
		return nil, os.ErrNotExist
	}
	cmd, err := backend.Attach(name, nil, nil, "")
	if err != nil || cmd == nil {
		return nil, os.ErrInvalid
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	g := &gravador{cmd: cmd, ptmx: ptmx}

	tee, sessao, id, soltar := pegarLogDaSessao(dataDir, user, name)
	// The server screen: the same stream, passed through an emulator, so that the
	// lines LEAVING it become the session's rendered history. An observer, never a
	// middleman — see `historico.go`.
	tela := novaTelaDaSessao(dataDir, user, name)
	g.tela = tela
	if sessao != nil {
		// Obeys the session's size without having an opinion about it — see the
		// header. The server screen has to stay at the SAME size as the pty: that is
		// what makes the program's `ESC[nA` land on the line it meant, and it is why
		// the history comes out without repeated copies.
		aplicar := func(cols, rows uint16) {
			if cols < 2 || rows < 1 {
				return
			}
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
			tela.redimensiona(cols, rows)
		}
		if c, r := sessao.registraAplicador(id, aplicar); c > 0 && r > 0 {
			aplicar(c, r)
		}
	}

	go func() {
		defer func() {
			soltar()
			tela.fecha()
			g.para()
			gravadoresMu.Lock()
			if atual := gravadores[chave]; atual == g {
				delete(gravadores, chave)
			}
			gravadoresMu.Unlock()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				// The log FIRST. The screen is observation: if the emulator dies, the
				// raw record is still standing.
				_, _ = tee.Write(buf[:n])
				tela.alimenta(buf[:n])
				// The notice to subscribers (connections in frame mode) goes OUTSIDE the
				// screen's lock — see [telaDaSessao.escoaAviso].
				tela.escoaAviso()
			}
			if err != nil {
				// Normal end: the session died and the `dtach -a` went with it.
				return
			}
		}
	}()
	return g, nil
}

// telaDe returns a session's server screen, or nil when there is no live
// recorder for it (a dead session, or one never attached since the last boot).
// nil is a legitimate answer: the on-disk history still holds, there is just no
// snapshot of the live screen to add to it.
func telaDe(dataDir, user, name string) *telaDaSessao {
	if dataDir == "" || user == "" || name == "" {
		return nil
	}
	chave := sessionLogPath(dataDir, user, name)
	gravadoresMu.Lock()
	defer gravadoresMu.Unlock()
	if g := gravadores[chave]; g != nil {
		return g.tela
	}
	return nil
}

// PararGravador shuts down a session's recorder. Used when its log stops being
// that file — today, on a rename: the name changes, the log path changes, and an
// old recorder would keep feeding the file of a name that no longer exists. The
// new name's recorder comes up on the next attach.
func PararGravador(dataDir, user, name string) {
	if dataDir == "" || user == "" || name == "" {
		return
	}
	chave := sessionLogPath(dataDir, user, name)
	gravadoresMu.Lock()
	g := gravadores[chave]
	delete(gravadores, chave)
	gravadoresMu.Unlock()
	if g != nil {
		g.para()
	}
}

// GaranteGravadoresDasSessoesVivas starts the recorder of every session that
// already exists. Called at boot: without it, a session that survived a deploy
// would have no recorder until somebody opened a tab on it — and the hole in the
// log would come back in exactly the window where nobody is watching.
// `primario` receives the sessions with no registered owner: by the adoption rule
// (`config.Primary`), a session without a prefix belongs to its pool and only it
// can adopt the session — so the log is its log. Without that, a session in those
// conditions had no recorder until someone attached, which is the usual hole.
func GaranteGravadoresDasSessoesVivas(dataDir string, reg *Registry, own *Ownership, primario string) {
	if dataDir == "" || own == nil {
		return
	}
	sessoes, err := NewSessionBackend(dataDir, reg).List()
	if err != nil {
		return
	}
	ligados := 0
	for _, s := range sessoes {
		nome, _ := s["name"].(string)
		if nome == "" {
			continue
		}
		dono := own.Owner(nome)
		if dono == "" || dono == AudienceAll {
			// With no registered owner the log would have no path — it is per user.
			// The adoption rule already answers whose it is: the primary's.
			dono = primario
		}
		if dono == "" {
			continue
		}
		GaranteGravador(dataDir, dono, nome, reg)
		ligados++
	}
	if ligados > 0 {
		log.Printf("[pty] log recorder attached to %d session(s) already alive", ligados)
	}
}
