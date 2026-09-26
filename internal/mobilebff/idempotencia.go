package mobilebff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Idempotencia stores the result of a mutation so that a RETRY of the same
// action does not execute it twice.
//
// # Why this exists
//
// The app's outbound queue (`FilaDeEnvio`, on Android) resends whatever it
// failed to deliver. Resending is only safe if the server can recognize that
// it has already seen that action — because **a timeout is indistinguishable
// from "it never arrived"**. Without that distinction, "rename the session"
// can happen twice, and the second rename either fails or renames the wrong
// session.
//
// That is why the app's queue used to accept ONE path only (sending a WhatsApp
// message, which proves idempotency through `client_msg_id` in the body). Its
// KDoc said, correctly, that opening the queue to everything else was "a
// SERVER change, not a client one". This is that server change.
//
// # What stays out
//
// Not every mutation should be re-executable later. Killing a session or
// firing a deploy ten minutes later, with nobody watching, is worse than
// failing right away: the world moved in the meantime. Those are deliberately
// left out — idempotency solves "it happened twice", it does not solve "it
// happened late".
//
// # Why it writes to disk
//
// A deploy restarts the process, and the gap between the action and the retry
// crosses restarts easily. A table living only in memory would lose exactly
// the keys that matter most — those of whoever was offline when the server
// came back up.
type Idempotencia struct {
	mu       sync.Mutex
	arquivo  string
	entradas map[string]entradaIdempotente
	agora    func() time.Time
}

type entradaIdempotente struct {
	// Body returned on the first run, repeated verbatim on the retry.
	Corpo string `json:"corpo"`
	// HTTP status of the first run.
	Status int   `json:"status"`
	Quando int64 `json:"quando_ms"`
}

const (
	// A key is good for one day. Short enough that the table does not grow
	// without end, and long enough to cover a device that spent the night
	// offline — which is the case the queue exists to serve.
	validadeIdempotencia = 24 * time.Hour

	// Cap on entries. Once exceeded, the oldest go. It is not a disk limit:
	// it is the same honesty limit the client queue has — ten thousand
	// pending actions mean something is wrong somewhere else.
	tetoIdempotencia = 10_000
)

// NovaIdempotencia loads (or creates) the table in dataDir.
func NovaIdempotencia(dataDir string) *Idempotencia {
	i := &Idempotencia{
		arquivo:  filepath.Join(dataDir, "mobile-idempotencia.json"),
		entradas: map[string]entradaIdempotente{},
		agora:    time.Now,
	}
	i.carregar()
	return i
}

// Lembrada returns the stored result for the key, if a valid one exists.
//
// The key MUST include the user: two accounts can generate the same client-side
// identifier, and a result leaking between them would be worse than having no
// idempotency at all.
func (i *Idempotencia) Lembrada(chave string) (corpo string, status int, ok bool) {
	if i == nil || chave == "" {
		return "", 0, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	e, existe := i.entradas[chave]
	if !existe {
		return "", 0, false
	}
	if i.agora().UnixMilli()-e.Quando > validadeIdempotencia.Milliseconds() {
		delete(i.entradas, chave)
		return "", 0, false
	}
	return e.Corpo, e.Status, true
}

// Lembrar stores the result of this run.
func (i *Idempotencia) Lembrar(chave, corpo string, status int) {
	if i == nil || chave == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entradas[chave] = entradaIdempotente{
		Corpo:  corpo,
		Status: status,
		Quando: i.agora().UnixMilli(),
	}
	i.podar()
	i.gravar()
}

// podar removes what expired and, if still over the cap, the oldest.
// Called with the lock held.
func (i *Idempotencia) podar() {
	limite := i.agora().UnixMilli() - validadeIdempotencia.Milliseconds()
	for k, e := range i.entradas {
		if e.Quando < limite {
			delete(i.entradas, k)
		}
	}
	for len(i.entradas) > tetoIdempotencia {
		maisVelha, quando := "", int64(1)<<62
		for k, e := range i.entradas {
			if e.Quando < quando {
				maisVelha, quando = k, e.Quando
			}
		}
		if maisVelha == "" {
			return
		}
		delete(i.entradas, maisVelha)
	}
}

func (i *Idempotencia) carregar() {
	b, err := os.ReadFile(i.arquivo)
	if err != nil {
		return
	}
	var lido map[string]entradaIdempotente
	if json.Unmarshal(b, &lido) != nil {
		// Corrupt file: starting empty is the right degradation. The worst
		// that happens is a retry re-executing; wedging the BFF because a
		// cache table fails to deserialize would be out of all proportion.
		return
	}
	i.entradas = lido
}

// gravar persists. Called with the lock held.
func (i *Idempotencia) gravar() {
	b, err := json.Marshal(i.entradas)
	if err != nil {
		return
	}
	tmp := i.arquivo + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	// Atomic rename: a process killed mid-write leaves the previous file
	// intact instead of a truncated JSON that the next load would discard
	// entirely.
	_ = os.Rename(tmp, i.arquivo)
}

// escrever is used by the tests to corrupt the file on purpose.
func escrever(caminho, conteudo string) error {
	return os.WriteFile(caminho, []byte(conteudo), 0o600)
}
