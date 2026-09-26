package docker

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

type ComposeProject struct {
	Name        string
	WorkingDir  string
	ConfigFiles string
	Status      string
	Services    []ComposeService
}

type ComposeService struct {
	Name        string
	ContainerID string
	Image       string
	State       string
	Status      string
	Ports       []string
}

func (c *Client) ListComposeProjects(ctx context.Context) ([]ComposeProject, error) {
	containers, err := c.c.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	projectMap := make(map[string]*ComposeProject)
	for _, ct := range containers {
		projectName, ok := ct.Labels["com.docker.compose.project"]
		if !ok || projectName == "" {
			continue
		}
		proj, exists := projectMap[projectName]
		if !exists {
			proj = &ComposeProject{
				Name:        projectName,
				WorkingDir:  ct.Labels["com.docker.compose.project.working_dir"],
				ConfigFiles: ct.Labels["com.docker.compose.project.config_files"],
			}
			projectMap[projectName] = proj
		}
		svc := ComposeService{
			Name:        ct.Labels["com.docker.compose.service"],
			ContainerID: ct.ID,
			Image:       ct.Image,
			State:       ct.State,
			Status:      ct.Status,
			Ports:       formatPorts(ct.Ports),
		}
		if svc.Name == "" {
			if len(ct.Names) > 0 {
				svc.Name = strings.TrimPrefix(ct.Names[0], "/")
			}
		}
		proj.Services = append(proj.Services, svc)
	}

	// Derive project status from service states
	result := make([]ComposeProject, 0, len(projectMap))
	for _, proj := range projectMap {
		proj.Status = deriveProjectStatus(proj.Services)
		result = append(result, *proj)
	}
	return result, nil
}

func deriveProjectStatus(services []ComposeService) string {
	if len(services) == 0 {
		return "unknown"
	}
	running, total := 0, len(services)
	for _, s := range services {
		if s.State == "running" {
			running++
		}
	}
	switch {
	case running == 0:
		return "stopped"
	case running == total:
		return "running"
	default:
		return "partial"
	}
}

func formatPorts(ports []types.Port) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		var s string
		if p.PublicPort != 0 {
			if p.IP != "" {
				s = p.IP + ":" + strconv.Itoa(int(p.PublicPort)) + "->" + strconv.Itoa(int(p.PrivatePort)) + "/" + p.Type
			} else {
				s = strconv.Itoa(int(p.PublicPort)) + "->" + strconv.Itoa(int(p.PrivatePort)) + "/" + p.Type
			}
		} else {
			s = strconv.Itoa(int(p.PrivatePort)) + "/" + p.Type
		}
		out = append(out, s)
	}
	return out
}

var composeAllowedActions = map[string]bool{
	"up": true, "down": true, "restart": true, "start": true, "stop": true,
	"pull": true, "logs": true, "ps": true, "build": true,
	"-d": true, "--detach": true, "--no-deps": true, "--build": true,
	"--remove-orphans": true, "--force-recreate": true, "--rmi": true,
	"local": true, "all": true, "-v": true, "--volumes": true,
}

func (c *Client) ComposeAction(project, workingDir, action string) (string, error) {
	tokens := strings.Fields(action)
	if len(tokens) == 0 {
		return "", fmt.Errorf("empty action")
	}
	for _, t := range tokens {
		if !composeAllowedActions[t] {
			return "", fmt.Errorf("action token not allowed: %q", t)
		}
	}
	for _, ch := range project {
		if ch < 0x20 || ch == ' ' || ch == ';' || ch == '|' || ch == '&' || ch == '$' || ch == '`' {
			return "", fmt.Errorf("invalid project name")
		}
	}
	args := []string{"compose", "-p", project}
	args = append(args, tokens...)
	cmd := exec.Command("docker", args...)
	if workingDir != "" {
		clean := filepath.Clean(workingDir)
		if !filepath.IsAbs(clean) || strings.Contains(clean, "..") {
			return "", fmt.Errorf("working_dir must be absolute and free of ..")
		}
		cmd.Dir = clean
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
