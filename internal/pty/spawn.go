package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/claudebin"
)

func validatedModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", nil
	}
	if !aimodel.Allowed(model) {
		return "", errors.New("invalid model: " + model)
	}
	return model, nil
}

func SpawnClaudeSession(sessionName, resumeUUID, claudeConfigDir, model string) (string, error) {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return "", errors.New("invalid session name")
	}
	argv := []string{claudebin.Path()}
	if resumeUUID != "" {
		if !isValidSessionUUID(resumeUUID) {
			return "", errors.New("invalid resume uuid")
		}
		argv = append(argv, "--resume", resumeUUID)
	}
	if m, err := validatedModel(model); err != nil {
		return "", err
	} else if m != "" {
		argv = append(argv, "--model", m)
	}
	argv = append(argv, permModeArgs(sessionName)...)
	if err := SessionCreateDetached(sessionName, argv, claudeConfigEnv(claudeConfigDir), ""); err != nil {
		return "", err
	}
	return sessionName, nil
}

func SpawnLoginShell(sessionName, claudeConfigDir string) (string, error) {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return "", errors.New("invalid session name")
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if claudeConfigDir != "" {
		return sessionName, SessionCreateDetached(sessionName, []string{shell, "-l"}, claudeConfigEnv(claudeConfigDir), "")
	}
	return sessionName, SessionCreateDetached(sessionName, []string{"env", "-u", "CLAUDE_CONFIG_DIR", shell, "-l"}, claudeConfigEnv(""), "")
}

const sessionTerm = "xterm-256color"

func claudeConfigEnv(dir string) []string {
	env := []string{"TERM=" + sessionTerm}
	if dir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+dir)
	}
	if p := claudebin.PathEnv(); p != "" {
		env = append(env, "PATH="+p)
	}
	return env
}

func SpawnClaudeWorkSession(sessionName, repoPath, initialPrompt, claudeConfigDir, model string) (string, error) {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return "", errors.New("invalid session name")
	}
	m, err := validatedModel(model)
	if err != nil {
		return "", err
	}
	argv := []string{claudebin.Path()}
	if m != "" {
		argv = append(argv, "--model", m)
	}
	argv = append(argv, permModeArgs(sessionName)...)

	prompt := strings.TrimSpace(initialPrompt)
	viaArgv := prompt != "" && len(prompt) <= maxArgvPrompt
	if viaArgv {
		argv = append(argv, prompt)
	}

	if err := SessionCreateDetached(sessionName, argv, claudeConfigEnv(claudeConfigDir), repoPath); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if prompt == "" || viaArgv {
		return sessionName, nil
	}

	waitClaudeReady(sessionName, 20*time.Second)
	time.Sleep(600 * time.Millisecond)
	if err := SessionPasteAndEnter(sessionName, prompt); err != nil {
		return sessionName, fmt.Errorf("paste prompt: %w", err)
	}
	return sessionName, nil
}

const maxArgvPrompt = 96 * 1024

func SpawnJiraWorkSession(sessionName, repoPath, initialPrompt, claudeConfigDir, model string) (string, error) {
	return SpawnClaudeWorkSession(sessionName, repoPath, initialPrompt, claudeConfigDir, model)
}

func waitClaudeReady(sessionName string, timeout time.Duration) bool {
	time.Sleep(3 * time.Second)
	return false
}

func writeTmpFile(prefix, content string) (string, error) {
	f, err := os.CreateTemp("", prefix+"*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func RestartClaudeSession(sessionName, claudeConfigDir, model string) error {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return errors.New("invalid session name")
	}
	argv := []string{claudebin.Path(), "--continue"}
	if m, err := validatedModel(model); err != nil {
		return err
	} else if m != "" {
		argv = append(argv, "--model", m)
	}
	argv = append(argv, permModeArgs(sessionName)...)
	_ = SessionKill(sessionName)
	time.Sleep(200 * time.Millisecond)
	return SessionCreateDetached(sessionName, argv, claudeConfigEnv(claudeConfigDir), "")
}

func isValidSessionUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func Exec(cmd string) (string, error) {
	c := exec.Command("/bin/bash", "-lc", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		if strings.TrimSpace(string(out)) == "" {
			return err.Error(), err
		}
		return string(out), err
	}
	return string(out), nil
}
