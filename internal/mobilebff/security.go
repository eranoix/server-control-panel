package mobilebff

import "github.com/danielgtaylor/huma/v2"

const BearerSchemeName = "bearerAuth"

func applyBearerSecurity(config *huma.Config) {
	if config.Components == nil {
		config.Components = &huma.Components{}
	}
	if config.Components.SecuritySchemes == nil {
		config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	config.Components.SecuritySchemes[BearerSchemeName] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
		Description: "Access token issued by POST /auth/login (or renewed by " +
			"POST /auth/refresh), sent as `Authorization: Bearer <token>`. " +
			"It is validated by auth.Middleware, the same one used by every authenticated panel route.",
	}
	config.OnAddOperation = append(config.OnAddOperation, requireBearer)
}

func requireBearer(_ *huma.OpenAPI, op *huma.Operation) {
	if op.Security != nil {
		return
	}
	op.Security = []map[string][]string{{BearerSchemeName: {}}}
}
