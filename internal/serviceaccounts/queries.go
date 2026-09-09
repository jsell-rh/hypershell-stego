package serviceaccounts

import (
	"strings"
	"unicode/utf8"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
)

// ListOptions follows the public account list contract. Search is literal.
type ListOptions struct {
	Page, Size                  int
	Status, Search, Sort, Order string
}

func (o ListOptions) validate() (string, string, error) {
	if o.Page < 1 || o.Page > 1000000 || o.Size < 1 || o.Size > 100 || len(o.Search) > 4096 || !utf8.ValidString(o.Search) || strings.ContainsRune(o.Search, 0) {
		return "", "", gateways.ErrInvalid
	}
	switch o.Status {
	case "", "provisioning", "ready", "degraded", "expired", "revoking", "revoked", "deleting", "error":
	default:
		return "", "", gateways.ErrInvalid
	}
	column := map[string]string{"": "created_time", "created_at": "created_time", "expires_at": "expires_at", "name": "name", "role": "role", "status": "status"}[o.Sort]
	order := o.Order
	if order == "" {
		order = "desc"
	}
	if column == "" || (order != "asc" && order != "desc") {
		return "", "", gateways.ErrInvalid
	}
	return column, order, nil
}
