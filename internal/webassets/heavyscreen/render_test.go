package heavyscreen

import (
	"testing"

	"server-control-panel/internal/webassets/pins"
)

func TestProxmoxScreenRendersInBrowser(t *testing.T) {
	for _, bundle := range []string{"min", "src"} {
		t.Run(bundle, func(t *testing.T) {
			pins.Run(t, "test-proxmox-render.mjs", "PANEL_RENDER_BUNDLE="+bundle)
		})
	}
}
