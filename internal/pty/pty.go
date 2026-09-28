package pty

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/gorilla/websocket"

	"server-control-panel/internal/wsorigin"
)

const (
	pongWait           = 45 * time.Second
	pingPeriod         = 25 * time.Second
	writeWait          = 10 * time.Second
	maxMessage         = 1 << 20
	readChunk          = 8 * 1024
	maxPauseDuration   = 30 * time.Second
	maxSessionsPerUser = 20
	clientExitWait     = 2 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     wsorigin.CheckSameHost,
}

type ctrlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Data string `json:"data,omitempty"`
	X    int    `json:"x,omitempty"`
}

func serverPriming(attach, replay string) (sendHistory, forceRepaint bool) {
	freshAttach := attach != "1"
	clientRebuildsScreen := replay == "0"
	return freshAttach && !clientRebuildsScreen, freshAttach
}

func HostShell(w http.ResponseWriter, r *http.Request, user string, primary bool, own *Ownership, claudeConfigDir string, dataDir string, reg *Registry) {
	conn, err := upgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	backend := NewSessionBackend(dataDir, reg)

	user = safeSessionName(user)
	if user == "" {
		user = "anon"
	}
	sessionName := safeSessionName(r.URL.Query().Get("name"))
	if sessionName == "" {
		sessionName = safeSessionName(r.URL.Query().Get("tab"))
	}
	if sessionName == "" {
		sessionName = "main"
	}

	if r.URL.Query().Get("attach") == "1" {
		owner := own.Owner(sessionName)
		if owner == user || owner == AudienceAll || primary {
			if alive, _ := backend.Has(sessionName); !alive {
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(4404, "session ended"),
					time.Now().Add(writeWait))
				return
			}
		} else {
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
	}

	owner := own.Owner(sessionName)
	switch {
	case owner == user || owner == AudienceAll:
	case owner != "" && owner != user:
		if !primary {
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
	default:
		exists, _ := backend.Has(sessionName)
		if exists && !primary {
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
		if !exists {
			current := ownedSessionCount(own, user)
			if current >= maxSessionsPerUser {
				conn.WriteMessage(websocket.TextMessage,
					[]byte("session limit reached (cap "+strconv.Itoa(maxSessionsPerUser)+") — close one first"))
				return
			}
		}
		if err := own.Claim(sessionName, user); err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte("session not found"))
			return
		}
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var sessionEnv []string
	if claudeConfigDir != "" {
		sessionEnv = append(sessionEnv, "CLAUDE_CONFIG_DIR="+claudeConfigDir)
	}
	cmd, aerr := backend.Attach(sessionName, []string{shell, "-l"}, sessionEnv, "")
	if aerr != nil || cmd == nil {
		cmd = exec.Command(shell, "-l")
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if claudeConfigDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+claudeConfigDir)
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("failed to start pty: "+err.Error()))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Signal(syscall.SIGHUP)
		ended := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(ended)
		}()
		select {
		case <-ended:
			return
		case <-time.After(clientExitWait):
		}
		log.Printf("[pty] client did not exit on SIGHUP, killing it (session %q)", sessionName)
		_ = cmd.Process.Kill()
		select {
		case <-ended:
		case <-time.After(clientExitWait):
			log.Printf("[pty] client survived SIGKILL (session %q) — releasing the slot anyway", sessionName)
		}
	}()

	var tee io.Writer
	var sharedSession *sharedLog
	var connID int64
	if dataDir != "" {
		slog, shared, id, release := acquireSessionLog(dataDir, user, sessionName)
		defer release()
		tee = slog
		sharedSession = shared
		connID = id
		EnsureRecorder(dataDir, user, sessionName, reg)
	}

	sendHistory, forceRepaint := serverPriming(
		r.URL.Query().Get("attach"),
		r.URL.Query().Get("replay"),
	)
	clientPrimesOwnScreen := r.URL.Query().Get("replay") == "0"

	if dataDir != "" && sendHistory {
		if rep := attachReplay(dataDir, user, sessionName); len(rep) > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteMessage(websocket.BinaryMessage, rep)
		}
	}

	var (
		szMu               sync.Mutex
		lastCols, lastRows uint16
		notifyClient       func(uint16, uint16)
		writeToClient      func([]byte) error
		repaintOnce        sync.Once
	)

	acceptsFrame := r.URL.Query().Get("frame") == "1"
	var (
		frameMu          sync.Mutex
		frame            *clientFrame
		winCols, winRows uint16
		scrolledTotal    int
		stopFrame        func()
	)
	frameSignal := make(chan struct{}, 1)
	inFrameMode := func() bool {
		frameMu.Lock()
		defer frameMu.Unlock()
		return frame != nil
	}

	applySize := func(cols, rows uint16) {
		if cols < 2 || rows < 1 {
			return
		}
		szMu.Lock()
		lastCols, lastRows = cols, rows
		notifyFn := notifyClient
		szMu.Unlock()
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})

		frameMu.Lock()
		jc, jr := winCols, winRows
		smallerThanSession := acceptsFrame && jc >= 2 && jr >= 1 && (jc < cols || jr < rows)
		if smallerThanSession {
			if frame == nil {
				frame = newClientFrame(int(jc), int(jr))
			} else {
				frame.resize(int(jc), int(jr))
			}
		} else if frame != nil {
			frame = nil
		}
		inFrame := frame != nil
		frameMu.Unlock()

		if inFrame {
			if notifyFn != nil {
				notifyFn(jc, jr)
			}
			select {
			case frameSignal <- struct{}{}:
			default:
			}
			return
		}
		if notifyFn != nil {
			notifyFn(cols, rows)
		}
	}
	if acceptsFrame && dataDir != "" {
		screen := screenOf(dataDir, user, sessionName)
		if screen != nil {
			unsubscribe := screen.subscribe(func(scrolled int) {
				frameMu.Lock()
				scrolledTotal += scrolled
				frameMu.Unlock()
				select {
				case frameSignal <- struct{}{}:
				default:
				}
			})
			frameDone := make(chan struct{})
			stopFrame = func() {
				unsubscribe()
				close(frameDone)
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[pty] frame pump of session %q died: %v", sessionName, r)
					}
				}()
				for {
					select {
					case <-frameDone:
						return
					case <-frameSignal:
					}
					time.Sleep(16 * time.Millisecond)

					frameMu.Lock()
					q := frame
					scrolled := scrolledTotal
					scrolledTotal = 0
					frameMu.Unlock()
					if q == nil {
						continue
					}
					szMu.Lock()
					writeOut := writeToClient
					szMu.Unlock()
					if writeOut == nil {
						continue
					}
					var output []byte
					if b := q.scrolled(scrolled); len(b) > 0 {
						output = append(output, b...)
					}
					grade, cur, visible := screen.screenAndCursor()
					if b := q.update(grade, cur, visible); len(b) > 0 {
						output = append(output, b...)
					}
					if len(output) > 0 {
						if err := writeOut(output); err != nil {
							return
						}
					}
				}
			}()
		}
	}

	if sharedSession != nil {
		if c, r := sharedSession.registerApplier(connID, applySize); c > 0 && r > 0 {
			applySize(c, r)
		}
	}
	wobble := func() {
		time.Sleep(180 * time.Millisecond)
		szMu.Lock()
		c, rw := lastCols, lastRows
		szMu.Unlock()
		if c == 0 || rw == 0 {
			if ws, err := pty.GetsizeFull(ptmx); err == nil {
				c, rw = ws.Cols, ws.Rows
			}
		}
		if c == 0 || rw < 2 {
			return
		}
		bigger := pty.Winsize{Cols: c, Rows: rw + 1}
		_ = pty.Setsize(ptmx, &bigger)
		time.Sleep(350 * time.Millisecond)
		szMu.Lock()
		c2, r2 := lastCols, lastRows
		szMu.Unlock()
		if c2 == 0 || r2 == 0 {
			c2, r2 = c, rw
		}
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: c2, Rows: r2})
		log.Printf("[pty] repaint wobble on attach: %dx%d → %dx%d → %dx%d (session %q)", c, rw, bigger.Cols, bigger.Rows, c2, r2, sessionName)
	}
	defer func() {
		if stopFrame != nil {
			stopFrame()
		}
	}()

	attachDone := make(chan struct{})
	defer close(attachDone)
	go func() {
		select {
		case <-time.After(600 * time.Millisecond):
			if forceRepaint {
				repaintOnce.Do(func() { go wobble() })
			}
		case <-attachDone:
		}
	}()

	wantsNotice := r.URL.Query().Get("size") == "1"

	var reqCols, reqRows uint16
	proxy(conn, ptmx, func(cols, rows uint16) {
		if reqRows != 0 && (reqCols != cols || reqRows != rows) {
			log.Printf("[pty] client resize: %dx%d → %dx%d (session %q)", reqCols, reqRows, cols, rows, sessionName)
		}
		reqCols, reqRows = cols, rows
		frameMu.Lock()
		winCols, winRows = cols, rows
		if frame != nil {
			frame.resize(int(cols), int(rows))
		}
		frameMu.Unlock()
		if sharedSession == nil {
			applySize(cols, rows)
		} else {
			sessionLogsMu.Lock()
			effCols, effRows, changed, appliers := sharedSession.registerSize(connID, cols, rows, acceptsFrame)
			sessionLogsMu.Unlock()
			if changed {
				if effCols != cols || effRows != rows {
					log.Printf("[pty] effective size (smallest across clients): %dx%d (session %q)",
						effCols, effRows, sessionName)
				}
				for _, apply := range appliers {
					apply(effCols, effRows)
				}
			} else {
				applySize(effCols, effRows)
			}
		}
		if forceRepaint {
			repaintOnce.Do(func() { go wobble() })
		}
	}, tee, func(notifyFn func(uint16, uint16), writeOut func([]byte) error) {
		szMu.Lock()
		writeToClient = writeOut
		szMu.Unlock()
		if !wantsNotice {
			return
		}
		szMu.Lock()
		notifyClient = notifyFn
		cols, rows := lastCols, lastRows
		szMu.Unlock()
		if cols > 0 && rows > 0 && !acceptsFrame && !inFrameMode() {
			notifyFn(cols, rows)
		}
	}, func(p []byte, first bool) []byte {
		if inFrameMode() {
			return nil
		}
		if first && clientPrimesOwnScreen {
			return bytes.TrimPrefix(p, dtachAttachClear)
		}
		return p
	}, func(x int) {
		frameMu.Lock()
		q := frame
		frameMu.Unlock()
		if q == nil {
			return
		}
		sessionCols := 0
		if dataDir != "" {
			if screen := screenOf(dataDir, user, sessionName); screen != nil {
				sessionCols, _ = screen.size()
			}
		}
		frameMu.Lock()
		q.shift(x, sessionCols)
		frameMu.Unlock()
		select {
		case frameSignal <- struct{}{}:
		default:
		}
	})
}

func ContainerShell(w http.ResponseWriter, r *http.Request, cli *dockerclient.Client, id string) {
	ContainerExec(w, r, cli, id, nil, nil)
}

func ContainerExec(w http.ResponseWriter, r *http.Request, cli *dockerclient.Client, id string, cmd []string, env []string) {
	conn, err := upgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	ctx := context.Background()

	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash -l || exec sh"}
	}
	env = append([]string{"TERM=xterm-256color"}, env...)

	exec, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		AttachStdin: true, AttachStdout: true, AttachStderr: true, Tty: true,
		Cmd: cmd,
		Env: env,
	})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("exec create: "+err.Error()))
		return
	}
	attach, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("exec attach: "+err.Error()))
		return
	}
	defer attach.Close()

	proxy(conn, wsFromHijacked{attach.Conn, attach.Reader}, func(cols, rows uint16) {
		_ = cli.ContainerExecResize(ctx, exec.ID, container.ResizeOptions{Width: uint(cols), Height: uint(rows)})
	}, nil, nil, nil, nil)
}

type wsFromHijacked struct {
	w io.WriteCloser
	r io.Reader
}

func (h wsFromHijacked) Read(p []byte) (int, error)  { return h.r.Read(p) }
func (h wsFromHijacked) Write(p []byte) (int, error) { return h.w.Write(p) }
func (h wsFromHijacked) Close() error                { return h.w.Close() }

type resizer func(cols, rows uint16)

func sizeIsSane(cols, rows uint16) bool {
	return cols >= 2 && rows >= 1 && cols <= 1000 && rows <= 1000
}

type sizeNotice struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type outputFilter func(p []byte, first bool) []byte

func proxy(conn *websocket.Conn, rwc io.ReadWriteCloser, resize resizer, tee io.Writer, onWritable func(notifySize func(uint16, uint16), writeBytes func([]byte) error), filterFn outputFilter, shift func(int)) {
	conn.SetReadLimit(maxMessage)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	defer registerLive(conn)()

	var mu sync.Mutex
	writeBinary := func(b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.BinaryMessage, b)
	}
	if onWritable != nil {
		onWritable(func(cols, rows uint16) {
			b, err := json.Marshal(sizeNotice{Type: "size", Cols: cols, Rows: rows})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}, writeBinary)
	}
	writePing := func() error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.PingMessage, nil)
	}

	done := make(chan struct{})
	closeOnce := sync.Once{}
	shutdown := func() {
		closeOnce.Do(func() {
			_ = rwc.Close()
			close(done)
		})
	}

	var (
		fcMu     sync.Mutex
		fcPaused bool
		fcResume = make(chan struct{})
	)
	setPaused := func(p bool) {
		fcMu.Lock()
		if p && !fcPaused {
			fcPaused = true
			fcResume = make(chan struct{})
		} else if !p && fcPaused {
			fcPaused = false
			close(fcResume)
		}
		fcMu.Unlock()
	}
	waitIfPaused := func() {
		for {
			fcMu.Lock()
			if !fcPaused {
				fcMu.Unlock()
				return
			}
			ch := fcResume
			fcMu.Unlock()
			select {
			case <-ch:
			case <-done:
				return
			case <-time.After(maxPauseDuration):
				setPaused(false)
				log.Printf("[pty] flow control: auto-resume after %v paused (the client never resumed)", maxPauseDuration)
				return
			}
		}
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[pty] PTY->WS pump panic recovered: %v", r)
				shutdown()
			}
		}()
		buf := make([]byte, readChunk)
		firstBlock := true
		for {
			waitIfPaused()
			n, err := rwc.Read(buf)
			if n > 0 {
				if tee != nil {
					_, _ = tee.Write(buf[:n])
				}
				output := buf[:n]
				if filterFn != nil {
					output = filterFn(output, firstBlock)
				}
				firstBlock = false
				if len(output) > 0 {
					if werr := writeBinary(output); werr != nil {
						shutdown()
						return
					}
				}
			}
			if err != nil {
				shutdown()
				return
			}
		}
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[pty] ping ticker panic recovered: %v", r)
				shutdown()
			}
		}()
		t := time.NewTicker(pingPeriod)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := writePing(); err != nil {
					shutdown()
					return
				}
			}
		}
	}()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			shutdown()
			return
		}
		switch mt {
		case websocket.TextMessage:
			if len(data) > 0 && data[0] == '{' {
				var m ctrlMsg
				if jerr := json.Unmarshal(data, &m); jerr == nil {
					switch m.Type {
					case "resize":
						if resize != nil && sizeIsSane(m.Cols, m.Rows) {
							resize(m.Cols, m.Rows)
						}
					case "pan":
						if shift != nil {
							shift(m.X)
						}
					case "input":
						_, _ = rwc.Write([]byte(m.Data))
					case "paste":
						_, _ = rwc.Write([]byte("\x1b[200~" + m.Data + "\x1b[201~"))
					case "pause":
						setPaused(true)
					case "resume":
						setPaused(false)
					case "ping":
						_ = writeBinary(nil)
					}
					continue
				}
			}
			_, _ = rwc.Write(data)
		case websocket.BinaryMessage:
			_, _ = rwc.Write(data)
		}
	}
}
