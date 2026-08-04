package app

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// initExtAuth initializes external JWT auth from ServerConfig. Returns
// the middleware (nil if unconfigured) and a cleanup function for the JWKS cache.
func initExtAuth(config webui.ServerConfig) (middleware.Middleware, func()) {
	if config.ExtAuthURL == "" {
		return nil, nil
	}
	jwksURL := config.ExtAuthURL + "/api/auth/jwks"
	var jwksClient *http.Client
	if config.ExtAuthAllowInsecure {
		jwksClient = middleware.NewJWKSHTTPClient(webui.SafeDialContext(true))
	} else {
		jwksClient = middleware.NewJWKSHTTPClient(webui.SafeDialContext(false))
	}
	jwksCache := middleware.NewJWKSCache(jwksURL, jwksClient, config.Logger)

	mw := middleware.Auth(middleware.AuthConfig{
		JWKSCache: jwksCache,
		Issuer:    config.ExtAuthIssuer,
		Audience:  config.ExtAuthAudience,
		Logger:    config.Logger,
	})
	logger.Info("external auth enabled",
		"component", "auth",
		"auth_url", config.ExtAuthURL,
		"jwks_url", jwksURL,
		"issuer", config.ExtAuthIssuer,
	)
	return mw, jwksCache.Stop
}
