package contracts

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

//go:embed extensions/openapi*.yaml extensions/current-user.openapi.yaml
var activeOpenAPI embed.FS

// LoadActiveOpenAPI resolves the current REST contract from embedded files.
// The captured reference remains available through Load.
func LoadActiveOpenAPI(ctx context.Context) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		if location.Scheme != "" || location.Host != "" || location.RawQuery != "" || !fs.ValidPath(location.Path) || !strings.HasPrefix(location.Path, "extensions/") {
			return nil, fmt.Errorf("active OpenAPI reference is outside its embedded files")
		}
		return activeOpenAPI.ReadFile(location.Path)
	}
	document, err := loader.LoadFromFile("extensions/openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("load active OpenAPI: %w", err)
	}
	if err = document.Validate(ctx); err != nil {
		return nil, fmt.Errorf("invalid active OpenAPI: %w", err)
	}
	return document, nil
}
