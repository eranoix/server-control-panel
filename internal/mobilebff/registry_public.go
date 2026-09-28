package mobilebff

import (
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type publicNamedRegistrar struct {
	name string
	fn   Registrar
}

var publicRegistrars []publicNamedRegistrar

func RegisterPublic(name string, fn Registrar) {
	for _, r := range publicRegistrars {
		if r.name == name {
			panic("mobilebff: duplicate public registration: " + name)
		}
	}
	publicRegistrars = append(publicRegistrars, publicNamedRegistrar{name: name, fn: fn})
}

func MountPublic(mux *http.ServeMux, deps Deps) huma.API {
	config := huma.DefaultConfig("server-control-panel mobile BFF (public)", "1.0.0")
	api := humago.NewWithPrefix(mux, Prefix, config)

	sorted := make([]publicNamedRegistrar, len(publicRegistrars))
	copy(sorted, publicRegistrars)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	for _, r := range sorted {
		r.fn(api, deps)
	}

	return api
}
