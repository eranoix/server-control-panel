package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/caddyserver/certmagic"

	"server-control-panel/internal/api"
	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/deploy"
	"server-control-panel/internal/httpmw"
	ptysvc "server-control-panel/internal/pty"
	"server-control-panel/internal/secrets"
	"server-control-panel/internal/webassets"
)

var buildVersion = "dev"

func main() {
	var (
		flagCheck   = flag.Bool("check", false, "validate config + subsystems without binding; exits 0 if healthy")
		flagVersion = flag.Bool("version", false, "print build version and exit")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Println(buildVersion)
		return
	}

	if *flagCheck {
		if err := runChecks(); err != nil {
			fmt.Fprintf(os.Stderr, "CHECK FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK")
		return
	}

	if rest := flag.Args(); len(rest) >= 1 && rest[0] == "run-job" {
		id := ""
		if len(rest) >= 2 {
			id = rest[1]
		}
		if err := runDetachedJob(id); err != nil {
			log.Fatalf("run-job %s: %v", id, err)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if cfg.SchemaVersion > config.CurrentSchemaVersion {
		log.Fatalf("config schema_version=%d is from a newer server-control-panel (this binary speaks v%d); downgrade not supported", cfg.SchemaVersion, config.CurrentSchemaVersion)
	}

	if cfg.SchemaVersion < config.CurrentSchemaVersion {
		var vault *secrets.Store
		if cfg.JWTSecret != "" {
			if v, vErr := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret); vErr == nil {
				vault = v
			} else {
				log.Printf("migrate: secrets vault unavailable (%v) — migration will skip vault re-key", vErr)
			}
		}
		var migAudit *auth.AuditLog
		if al, aErr := auth.NewAuditLog(filepath.Join(cfg.DataDir, "audit.log")); aErr == nil {
			migAudit = al
		}
		mErr := config.MigrateV1ToV2(config.MigrationDeps{
			Cfg:        cfg,
			ConfigPath: filepath.Join(cfg.DataDir, "config.json"),
			DataDir:    cfg.DataDir,
			Vault:      vault,
			Audit:      migAudit,
			Primary:    "sam",
		})
		if mErr != nil {
			log.Fatalf("migrate v1→v2: %v (backup at %s.bak.<ts>)", mErr, cfg.DataDir)
		}
		if reloaded, lErr := config.Load(); lErr == nil {
			cfg = reloaded
		} else {
			log.Fatalf("config reload after migration: %v", lErr)
		}
	}

	if mErr := deploy.MigrateApps(deploy.AppsMigration{
		DataDir: cfg.DataDir,
		Audit:   migrationAudit(cfg.DataDir),
	}); mErr != nil {
		log.Fatalf("migrate apps v1→v2: %v (backup at %s/deploy/apps.json.bak.<ts>)", mErr, cfg.DataDir)
	}

	router, err := api.NewRouter(cfg)
	if err != nil {
		log.Fatalf("router: %v", err)
	}
	router.StartBackgroundWorkers(context.Background())

	srv := &http.Server{
		Addr:        cfg.Listen,
		Handler:     router,
		IdleTimeout: 120 * time.Second,
	}

	go webassets.WarmPrecompression()

	go func() {
		switch {
		case cfg.TLSEnabled && cfg.TLSDomain != "":
			tlsCfg, err := setupACME(cfg)
			if err != nil {
				log.Fatalf("acme: %v", err)
			}
			srv.TLSConfig = tlsCfg
			log.Printf("server-control-panel listening TLS (Let's Encrypt %s) on %s", cfg.TLSDomain, cfg.Listen)
			if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen tls (acme): %v", err)
			}
		case cfg.TLSEnabled:
			cert, key := cfg.TLSCert, cfg.TLSKey
			if cert == "" || key == "" {
				cert = filepath.Join(cfg.DataDir, "tls.crt")
				key = filepath.Join(cfg.DataDir, "tls.key")
			}
			if _, err := os.Stat(cert); os.IsNotExist(err) {
				if err := generateSelfSigned(cert, key); err != nil {
					log.Fatalf("gen tls: %v", err)
				}
				log.Printf("generated self-signed cert: %s", cert)
			}
			log.Printf("server-control-panel listening TLS on %s", cfg.Listen)
			if err := srv.ListenAndServeTLS(cert, key); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen tls: %v", err)
			}
		default:
			log.Printf("server-control-panel listening on %s", cfg.Listen)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("listen: %v", err)
			}
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	reload := make(chan os.Signal, 1)
	signal.Notify(reload, syscall.SIGHUP)
	go func() {
		for range reload {
			log.Printf("SIGHUP received: re-reading config.json (Listen/TLS require a full restart)")
			if newCfg, err := config.Load(); err != nil {
				log.Printf("config reload failed: %v (keeping the current config)", err)
			} else {
				log.Printf("config reload OK: schema=%d users=%d backend=%s",
					newCfg.SchemaVersion, len(newCfg.AllUsers()), newCfg.AuthBackend)
			}
		}
	}()
	<-stop
	if n := ptysvc.NotifyRestart(); n > 0 {
		log.Printf("shutdown: warned %d connected terminal(s) about the restart", n)
	}
	log.Println("shutting down — draining connections (up to 15s)")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	shutdownACME(ctx)
	httpmw.BWShutdown()
	router.Shutdown(ctx)
	log.Println("shutdown complete")
}

func setupACME(cfg *config.Config) (*tls.Config, error) {
	storage := &certmagic.FileStorage{Path: filepath.Join(cfg.DataDir, "certmagic")}
	certmagic.Default.Storage = storage
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = cfg.TLSEmail
	certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA

	magic := certmagic.NewDefault()
	if err := magic.ManageAsync(context.Background(), []string{cfg.TLSDomain}); err != nil {
		return nil, err
	}

	issuer := certmagic.NewACMEIssuer(magic, certmagic.DefaultACME)
	mux := http.NewServeMux()
	mux.Handle("/", issuer.HTTPChallengeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + cfg.TLSDomain + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})))
	acmeChallengeServer := &http.Server{Addr: ":80", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	acmeServers = append(acmeServers, acmeChallengeServer)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("acme listener panic recovered: %v", r)
			}
		}()
		if err := acmeChallengeServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("acme challenge listener (:80) failed: %v — Let's Encrypt issuance may fail until :80 is reachable", err)
		}
	}()

	return magic.TLSConfig(), nil
}

var acmeServers []*http.Server

func shutdownACME(ctx context.Context) {
	for _, s := range acmeServers {
		_ = s.Shutdown(ctx)
	}
}

func generateSelfSigned(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "server-control-panel"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	cf, err := os.OpenFile(certPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer cf.Close()
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return err
	}
	keyDer, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	kf, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer kf.Close()
	return pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})
}

func runChecks() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if len(cfg.AllUsers()) == 0 {
		return fmt.Errorf("config has zero users (Username/PasswordHash and Users[] are both empty)")
	}
	if cfg.JWTSecret == "" {
		return fmt.Errorf("config: jwt_secret is empty")
	}
	auditPath := filepath.Join(cfg.DataDir, "audit.log")
	if af, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = af.Close()
	} else {
		return fmt.Errorf("audit log %s not writable: %w", auditPath, err)
	}
	if cfg.TLSEnabled && cfg.TLSDomain == "" && cfg.TLSCert != "" {
		if _, err := os.Stat(cfg.TLSCert); err != nil {
			return fmt.Errorf("tls_cert %s missing: %w", cfg.TLSCert, err)
		}
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		if !isAddrInUse(err) {
			return fmt.Errorf("listen test on %s failed: %w", cfg.Listen, err)
		}
	} else {
		_ = ln.Close()
	}
	if cfg.LoadedFromBackup {
		fmt.Fprintf(os.Stderr, "WARN: config was recovered from %s — live config.json may still be corrupt\n", cfg.LoadedBackupName)
	}
	return nil
}

func isAddrInUse(err error) bool {
	s := err.Error()
	return contains(s, "address already in use") || contains(s, "bind: address already in use")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func migrationAudit(dataDir string) *auth.AuditLog {
	al, err := auth.NewAuditLog(filepath.Join(dataDir, "audit.log"))
	if err != nil {
		log.Printf("migrate: audit unavailable (%v) — migration will proceed without an event", err)
		return nil
	}
	return al
}
