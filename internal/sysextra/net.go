package sysextra

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Port struct {
	Proto, Local, Peer, State, Process string
	PID                                int
}

type Conn struct {
	Proto, Local, Peer, State string
}

var (
	userProcRe = regexp.MustCompile(`\(\("([^"]+)",pid=(\d+)`)
)

func runSS(args ...string) ([]byte, error) {
	cmd := exec.Command("ss", args...)
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("ss not available")
		}
		if _, ok := err.(*exec.Error); ok {
			return nil, errors.New("ss not available")
		}
		return nil, fmt.Errorf("ss: %w", err)
	}
	return out, nil
}

func parseListening(out []byte, proto string) []Port {
	var ports []Port
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// Expected columns: State Recv-Q Send-Q Local Peer [users...]
		if len(fields) < 5 {
			continue
		}
		p := Port{
			Proto: proto,
			State: fields[0],
			Local: fields[3],
			Peer:  fields[4],
		}
		// users blob may be field 5 or later; search the rest of the line
		rest := ""
		if len(fields) >= 6 {
			rest = strings.Join(fields[5:], " ")
		}
		if m := userProcRe.FindStringSubmatch(rest); m != nil {
			p.Process = m[1]
			if pid, err := strconv.Atoi(m[2]); err == nil {
				p.PID = pid
			}
		}
		ports = append(ports, p)
	}
	return ports
}

func Listening() ([]Port, error) {
	tcpOut, err := runSS("-tlnpH")
	if err != nil {
		return nil, err
	}
	udpOut, err := runSS("-ulnpH")
	if err != nil {
		return nil, err
	}
	ports := parseListening(tcpOut, "tcp")
	ports = append(ports, parseListening(udpOut, "udp")...)
	return ports, nil
}

func Connections() ([]Conn, error) {
	out, err := runSS("-tnHo", "state", "established")
	if err != nil {
		return nil, err
	}
	var conns []Conn
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// Without state column: Recv-Q Send-Q Local Peer
		if len(fields) < 4 {
			continue
		}
		conns = append(conns, Conn{
			Proto: "tcp",
			State: "ESTAB",
			Local: fields[2],
			Peer:  fields[3],
		})
	}
	return conns, nil
}
