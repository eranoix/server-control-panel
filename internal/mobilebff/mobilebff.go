package mobilebff

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const Prefix = "/api/mobile/v1"

func Mount(mux *http.ServeMux, deps Deps) huma.API {
	config := huma.DefaultConfig("server-control-panel mobile BFF", "1.0.0")
	applyBearerSecurity(&config)
	api := humago.NewWithPrefix(mux, Prefix, config)

	runRegistrars(api, deps)

	return api
}
