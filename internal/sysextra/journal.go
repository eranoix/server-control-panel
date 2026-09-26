package sysextra

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type Unit struct {
	Name, Load, Active, Sub, Description string
}

var unitNameRe = regexp.MustCompile(`^[a-zA-Z0-9._@-]+$`)

func validateUnit(unit string) error {
	if !unitNameRe.MatchString(unit) {
		return errors.New("invalid unit name")
	}
	return nil
}

func ListUnits() ([]Unit, error) {
	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w", err)
	}
	var units []Unit
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		u := Unit{
			Name:   fields[0],
			Load:   fields[1],
			Active: fields[2],
			Sub:    fields[3],
		}
		if len(fields) > 4 {
			// Reconstruct description by finding position after the 4th field
			rest := line
			for i := 0; i < 4; i++ {
				rest = strings.TrimLeft(rest, " \t")
				idx := strings.IndexAny(rest, " \t")
				if idx < 0 {
					rest = ""
					break
				}
				rest = rest[idx:]
			}
			u.Description = strings.TrimSpace(rest)
		}
		units = append(units, u)
	}
	return units, nil
}

func Status(unit string) (string, error) {
	if err := validateUnit(unit); err != nil {
		return "", err
	}
	cmd := exec.Command("systemctl", "status", unit, "--no-pager", "--lines=0")
	out, _ := cmd.CombinedOutput()
	// systemctl status returns non-zero for inactive units, but output is still useful
	return string(out), nil
}

func Journal(unit string, lines int) ([]string, error) {
	if err := validateUnit(unit); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	cmd := exec.Command("journalctl", "-u", unit, "-n", fmt.Sprintf("%d", lines), "--no-pager", "--output=short-iso")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	raw := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	result := make([]string, 0, len(raw))
	for _, l := range raw {
		if l == "" {
			continue
		}
		result = append(result, l)
	}
	return result, nil
}

func SystemdRestart(unit string) (string, error) {
	if err := validateUnit(unit); err != nil {
		return "", err
	}
	cmd := exec.Command("systemctl", "restart", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("systemctl restart: %w", err)
	}
	return string(out), nil
}
