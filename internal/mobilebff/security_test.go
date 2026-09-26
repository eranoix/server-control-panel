package mobilebff

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// operationsOf returns every operation in the document, indexed by
// "METHOD /path" — the same granularity at which openapi-generator decides
// whether or not to emit authentication code for the call.
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

// TestMountDeclaresBearerScheme locks down the existence of the securityScheme
// in the document: without it openapi-generator emits no credential provider at
// all, and the Kotlin ApiClient goes back to being born with accessToken/
// accessTokenProvider/AUTHORIZATION declared and never used.
func TestMountDeclaresBearerScheme(t *testing.T) {
	api := Mount(http.NewServeMux(), Deps{})
	spec := api.OpenAPI()

	if spec.Components == nil || spec.Components.SecuritySchemes == nil {
		t.Fatal("components.securitySchemes missing from the authenticated surface's spec")
	}
	scheme, ok := spec.Components.SecuritySchemes[BearerSchemeName]
	if !ok {
		t.Fatalf("securityScheme %q ausente; presentes: %v", BearerSchemeName, spec.Components.SecuritySchemes)
	}
	if scheme.Type != "http" || scheme.Scheme != "bearer" {
		t.Errorf("securityScheme = {type:%q scheme:%q}, want {type:\"http\" scheme:\"bearer\"}", scheme.Type, scheme.Scheme)
	}
}

// TestMountMarksEveryOperationProtected makes sure a new endpoint cannot enter
// the authenticated BFF without declared security — the marking comes from the
// hook in applyBearerSecurity, not from each handlers_*.go remembering to do it.
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

// TestMountPublicDeclaresNoSecurity is the other side of the coin: the routes
// that sit OUTSIDE auth.Middleware (registry_public.go) must not declare
// security. A spec that lied here would make the generated client attach a
// Bearer that does not exist yet, precisely on /auth/login and /auth/refresh.
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

// TestRequireBearerRespeitaMarcacaoPrevia documents the escape hatch: a
// registrar that has already decided its own operation's security (including an
// empty slice, which in OpenAPI means "explicitly no security") is not overridden.
func TestRequireBearerRespeitaMarcacaoPrevia(t *testing.T) {
	semSeguranca := &huma.Operation{Security: []map[string][]string{}}
	requireBearer(nil, semSeguranca)
	if len(semSeguranca.Security) != 0 {
		t.Errorf("Security = %v, want to stay empty", semSeguranca.Security)
	}

	naoMarcada := &huma.Operation{}
	requireBearer(nil, naoMarcada)
	if !hasBearer(naoMarcada) {
		t.Errorf("Security = %v, want it to contain %q", naoMarcada.Security, BearerSchemeName)
	}
}
