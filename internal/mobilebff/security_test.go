package mobilebff

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func operationsOf(t *testing.T, spec *huma.OpenAPI) map[string]*huma.Operation {
	t.Helper()
	out := map[string]*huma.Operation{}
	for path, item := range spec.Paths {
		for method, op := range map[string]*huma.Operation{
			"GET":     item.Get,
			"PUT":     item.Put,
			"POST":    item.Post,
			"DELETE":  item.Delete,
			"OPTIONS": item.Options,
			"HEAD":    item.Head,
			"PATCH":   item.Patch,
			"TRACE":   item.Trace,
		} {
			if op != nil {
				out[method+" "+path] = op
			}
		}
	}
	return out
}

func hasBearer(op *huma.Operation) bool {
	for _, req := range op.Security {
		if _, ok := req[BearerSchemeName]; ok {
			return true
		}
	}
	return false
}

func TestMountDeclaresBearerScheme(t *testing.T) {
	api := Mount(http.NewServeMux(), Deps{})
	spec := api.OpenAPI()

	if spec.Components == nil || spec.Components.SecuritySchemes == nil {
		t.Fatal("components.securitySchemes missing from the authenticated surface's spec")
	}
	scheme, ok := spec.Components.SecuritySchemes[BearerSchemeName]
	if !ok {
		t.Fatalf("securityScheme %q missing; present: %v", BearerSchemeName, spec.Components.SecuritySchemes)
	}
	if scheme.Type != "http" || scheme.Scheme != "bearer" {
		t.Errorf("securityScheme = {type:%q scheme:%q}, want {type:\"http\" scheme:\"bearer\"}", scheme.Type, scheme.Scheme)
	}
}

func TestMountMarksEveryOperationProtected(t *testing.T) {
	api := Mount(http.NewServeMux(), Deps{})
	ops := operationsOf(t, api.OpenAPI())
	if len(ops) == 0 {
		t.Fatal("no operation registered — the test would not be verifying anything")
	}
	for name, op := range ops {
		if !hasBearer(op) {
			t.Errorf("%s: no security bearer declared (op.Security = %v)", name, op.Security)
		}
	}
}

func TestMountPublicDeclaresNoSecurity(t *testing.T) {
	api := MountPublic(http.NewServeMux(), Deps{})
	spec := api.OpenAPI()

	if spec.Components != nil && len(spec.Components.SecuritySchemes) != 0 {
		t.Errorf("public surface declared securitySchemes: %v", spec.Components.SecuritySchemes)
	}
	ops := operationsOf(t, spec)
	if len(ops) == 0 {
		t.Fatal("no public route registered — the test would not be verifying anything")
	}
	for name, op := range ops {
		if len(op.Security) != 0 {
			t.Errorf("%s: public route with security declared (%v)", name, op.Security)
		}
	}
}

func TestRequireBearerRespectsPriorMarking(t *testing.T) {
	noSecurity := &huma.Operation{Security: []map[string][]string{}}
	requireBearer(nil, noSecurity)
	if len(noSecurity.Security) != 0 {
		t.Errorf("Security = %v, want to stay empty", noSecurity.Security)
	}

	unmarked := &huma.Operation{}
	requireBearer(nil, unmarked)
	if !hasBearer(unmarked) {
		t.Errorf("Security = %v, want it to contain %q", unmarked.Security, BearerSchemeName)
	}
}
