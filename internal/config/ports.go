// ports.go — named constants for internal ports. They allow refactoring
// without a grep-and-replace across handlers and scripts. The production
// listeners (config.Listen) stay in config.json — these constants are for
// the internal upstreams the control plane orchestrates.
package config

const (
	// DefaultControlPlanePort — the port of the vps-manager binary (overridden
	// by config.Listen in data/config.json).
	DefaultControlPlanePort = 8765

	// V2ParallelPort — the port of vpsmanager-v2 running alongside (dev/staging).
	V2ParallelPort = 8766

	// PrivateAIPort (8787) and VeniceAIPort (8784) were removed — the app no
	// longer integrates the bridge/Venice. The private-ai-api service still
	// runs on the host.

	// COTURNPort — STUN/TURN for the video call (UDP+TCP).
	COTURNPort = 3478

	// STTProxyPort — the local Silero VAD + whisper.cpp orchestrator.
	STTProxyPort = 8082

	// WhisperServerPort — whisper.cpp HTTP server (upstream of the STT proxy).
	WhisperServerPort = 8081

	// BrowserProxyPort — Ultraviolet+Wisp tunnel for the embedded browser.
	BrowserProxyPort = 8090

	// NoVNCBasePort — base port for the noVNC of browser-instance-pro. Each
	// instance takes BasePort + N.
	NoVNCBasePort = 6901
)
