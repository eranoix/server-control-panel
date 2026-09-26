package caso

import "os/exec"

// Controles herdados do precedente da Fase 7. O agente VAI rodar docker
// legitimamente; o pino tem de aprovar explicitamente.
func diskUsedPct(path string) (pct int, err error) { return 0, nil }

func sobe() error {
	return exec.Command("docker", "compose", "up", "-d").Run()
}
