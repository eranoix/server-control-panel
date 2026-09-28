package pve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	consoleHandshakeTimeout = 10 * time.Second
	consoleOKTimeout        = 10 * time.Second
	consoleMaxFrame         = 256 << 10
	consoleMaxDim           = 10000
)

var ErrConsoleNoOK = errors.New("pve: console did not confirm the handshake (expected \"OK\")")

type ConsoleConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetReadLimit(limit int64)
	Close() error
}

type termproxyResp struct {
	Port   string `json:"port"`
	Ticket string `json:"ticket"`
	User   string `json:"user"`
	UPID   string `json:"upid"`
}

func (t *termproxyResp) UnmarshalJSON(raw []byte) error {
	var aux struct {
		Port   json.RawMessage `json:"port"`
		Ticket string          `json:"ticket"`
		User   string          `json:"user"`
		UPID   string          `json:"upid"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	t.Ticket, t.User, t.UPID = aux.Ticket, aux.User, aux.UPID
	t.Port = strings.Trim(strings.TrimSpace(string(aux.Port)), `"`)
	return nil
}

func (c *Client) ConsoleAttach(ctx context.Context, node string, vmid int, typ string) (ConsoleConn, string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return nil, "", err
	}
	return c.consoleAt(ctx, base)
}

func (c *Client) ConsoleAttachNode(ctx context.Context, node string) (ConsoleConn, string, error) {
	if node == "" {
		return nil, "", fmt.Errorf("pve: empty node in ConsoleAttachNode")
	}
	return c.consoleAt(ctx, "/api2/json/nodes/"+url.PathEscape(node))
}

func (c *Client) consoleAt(ctx context.Context, base string) (ConsoleConn, string, error) {
	var tp termproxyResp
	if err := c.do(ctx, http.MethodPost, base+"/termproxy", &tp); err != nil {
		return nil, "", err
	}
	if tp.Port == "" || tp.Ticket == "" || tp.User == "" {
		return nil, "", &Error{Kind: KindHypervisor, Path: base + "/termproxy",
			Err: errors.New("termproxy answered without port/ticket/user")}
	}

	wsURL, err := c.consoleWSURL(base, tp.Port, tp.Ticket)
	if err != nil {
		return nil, "", err
	}
	h := http.Header{}
	h.Set("Authorization", "PVEAPIToken="+c.tokenID+"="+c.secret)
	conn, resp, err := c.consoleDialer().DialContext(ctx, wsURL, h)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, "", &Error{Kind: kindFromStatus(status, err), Status: status,
			Path: base + "/vncwebsocket", Err: err}
	}
	conn.SetReadLimit(consoleMaxFrame)

	_ = conn.SetWriteDeadline(time.Now().Add(consoleOKTimeout))
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(tp.User+":"+tp.Ticket+"\n")); err != nil {
		_ = conn.Close()
		return nil, "", &Error{Kind: KindUnreachable, Path: base + "/vncwebsocket", Err: err}
	}
	_ = conn.SetReadDeadline(time.Now().Add(consoleOKTimeout))
	_, ack, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, "", &Error{Kind: KindUnreachable, Path: base + "/vncwebsocket", Err: err}
	}
	if strings.TrimSpace(string(ack)) != "OK" {
		_ = conn.Close()
		return nil, "", &Error{Kind: KindHypervisor, Path: base + "/vncwebsocket", Err: ErrConsoleNoOK}
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return conn, tp.UPID, nil
}

func (c *Client) consoleWSURL(base, port, ticket string) (string, error) {
	u, err := url.Parse(c.baseURL + base + "/vncwebsocket")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBaseURL, err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("%w: scheme %q", ErrBaseURL, u.Scheme)
	}
	u.RawQuery = url.Values{"port": {port}, "vncticket": {ticket}}.Encode()
	return u.String(), nil
}

func (c *Client) consoleDialer() *websocket.Dialer {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &websocket.Dialer{
		HandshakeTimeout: consoleHandshakeTimeout,
		Subprotocols:     []string{"binary"},
		TLSClientConfig:  c.tlsCfg.Clone(),
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, redirectAddr(addr, c.resolve))
		},
	}
}

func kindFromStatus(status int, err error) Kind {
	switch {
	case status == http.StatusUnauthorized:
		return KindNoCredential
	case status == http.StatusForbidden:
		return KindForbidden
	case status >= 400:
		return KindHypervisor
	case err != nil:
		return KindUnreachable
	default:
		return KindHypervisor
	}
}

func InputFrame(data []byte) []byte {
	out := make([]byte, 0, len(data)+8)
	out = append(out, "0:"...)
	out = strconv.AppendInt(out, int64(len(data)), 10)
	out = append(out, ':')
	return append(out, data...)
}

func ResizeFrame(cols, rows int) []byte {
	if cols <= 0 || rows <= 0 || cols > consoleMaxDim || rows > consoleMaxDim {
		return nil
	}
	return []byte("1:" + strconv.Itoa(cols) + ":" + strconv.Itoa(rows) + ":")
}

func KeepaliveFrame() []byte { return []byte("2") }

func clientTLSConfig(tr *http.Transport) *tls.Config { return tr.TLSClientConfig }
