package serviceaccounts

import (
	"encoding/json"
	"errors"
)

type oidc struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	Audience string `json:"audience"`
}

func parseOIDC(value *string) (oidc, error) {
	var result oidc
	if value == nil || len(*value) > 8192 {
		return result, errors.New("Gateway OIDC configuration is absent")
	}
	if err := json.Unmarshal([]byte(*value), &result); err != nil {
		return oidc{}, err
	}
	return result, nil
}
