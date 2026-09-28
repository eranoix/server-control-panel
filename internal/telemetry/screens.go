package telemetry

import (
	_ "embed"
	"strings"
)

//go:embed screens.txt
var screensRaw string

var screenList = func() []string {
	lines := strings.Split(strings.TrimSpace(screensRaw), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}()

var screenSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(screenList))
	for _, l := range screenList {
		m[l] = struct{}{}
	}
	return m
}()

func IsKnownScreen(id string) bool { _, ok := screenSet[id]; return ok }

func AllScreens() []string {
	out := make([]string, len(screenList))
	copy(out, screenList)
	return out
}
