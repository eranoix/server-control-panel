package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// nginxSitesDir is where we write the apps' vhosts. One file per compose
// project, prefixed vpsm-app- so it never collides with hand-written vhosts.
const nginxSitesDir = "/etc/nginx/sites-enabled"

const vhostTmpl = `# gerado pelo vps-manager (deploy PaaS) — não editar; regenerado a cada deploy.
server {
    listen 80;
    server_name %s;
    client_max_body_size 100m;
    location / {
        proxy_pass http://127.0.0.1:%d;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
    }
}
`

func vhostPath(project string) string {
	return filepath.Join(nginxSitesDir, "vpsm-app-"+project+".conf")
}

// writeVhost writes or updates the project's vhost, validates it with
// `nginx -t` and reloads. If validation fails it restores the previous state
// and returns an error (the deploy goes on — the container is already up, just
// without the domain proxy).
func writeVhost(ctx context.Context, project, domain string, port int, w io.Writer) error {
	if _, err := os.Stat(nginxSitesDir); err != nil {
		return fmt.Errorf("missing %s", nginxSitesDir)
	}
	path := vhostPath(project)
	prev, hadPrev := os.ReadFile(path)
	conf := fmt.Sprintf(vhostTmpl, domain, port)
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "nginx", "-t").CombinedOutput(); err != nil {
		// roll the file back
		if hadPrev == nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, prev, 0o644)
		}
		return fmt.Errorf("nginx -t: %s", out)
	}
	if out, err := exec.CommandContext(ctx, "nginx", "-s", "reload").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx reload: %s", out)
	}
	return nil
}

// removeVhost deletes the project's vhost and reloads nginx (app or preview
// teardown). Silent if it does not exist.
func removeVhost(ctx context.Context, project string) {
	path := vhostPath(project)
	if _, err := os.Stat(path); err != nil {
		return
	}
	_ = os.Remove(path)
	if out, err := exec.CommandContext(ctx, "nginx", "-t").CombinedOutput(); err == nil {
		_ = exec.CommandContext(ctx, "nginx", "-s", "reload").Run()
	} else {
		_ = out
	}
}
