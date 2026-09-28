package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"server-control-panel/internal/mobilebff"
	"server-control-panel/internal/mobilebff/screens"
)

const outputPath = "android/data/mobile-api-client/openapi/mobile-v1.yaml"

func main() {
	screens.Register(screens.SchedulerDeps{})

	screens.RegisterDocker(screens.DockerDeps{})

	screens.RegisterSystem(screens.SystemDeps{})

	screens.RegisterSecurity(screens.SecurityDeps{})
	screens.RegisterNetwork(screens.NetworkDeps{})

	screens.RegisterAlerts(screens.AlertsDeps{})

	screens.RegisterMisc(screens.MiscDeps{})

	protectedMux := http.NewServeMux()
	protectedAPI := mobilebff.Mount(protectedMux, mobilebff.Deps{})

	publicMux := http.NewServeMux()
	publicAPI := mobilebff.MountPublic(publicMux, mobilebff.Deps{})

	spec := protectedAPI.OpenAPI()
	publicSpec := publicAPI.OpenAPI()

	for path, item := range publicSpec.Paths {
		if _, dup := spec.Paths[path]; dup {
			log.Fatalf("mobile-openapi-gen: duplicate path between public and authenticated routes: %s", path)
		}
		spec.Paths[path] = item
	}
	if publicSpec.Components != nil && publicSpec.Components.Schemas != nil &&
		spec.Components != nil && spec.Components.Schemas != nil {
		dst := spec.Components.Schemas.Map()
		for name, schema := range publicSpec.Components.Schemas.Map() {
			if _, dup := dst[name]; dup {
				continue
			}
			dst[name] = schema
		}
	}

	yamlBytes, err := spec.DowngradeYAML()
	if err != nil {
		log.Fatalf("mobile-openapi-gen: downgrade to OpenAPI 3.0.3 failed: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		log.Fatalf("mobile-openapi-gen: creating the output directory: %v", err)
	}

	if err := os.WriteFile(outputPath, yamlBytes, 0o644); err != nil {
		log.Fatalf("mobile-openapi-gen: writing %s: %v", outputPath, err)
	}

	log.Printf("mobile-openapi-gen: %s generated (%d bytes)", outputPath, len(yamlBytes))
}
