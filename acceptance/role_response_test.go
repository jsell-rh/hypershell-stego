package acceptance

import (
	"encoding/json"
	"time"
)

// Keep the public Role shape independent of the generated response model.
// Existing discovery, grant, CLI, network, and restart checks use this shape.
type roleResponse struct {
	ID          string          `json:"id,omitempty"`
	Kind        string          `json:"kind"`
	Href        string          `json:"href"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Name        string          `json:"name"`
	DisplayName *string         `json:"display_name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Permissions json.RawMessage `json:"permissions,omitempty"`
	BuiltIn     bool            `json:"built_in"`
}

type roleListResponse struct {
	Kind  string         `json:"kind"`
	Href  string         `json:"href"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
	Total int64          `json:"total"`
	Items []roleResponse `json:"items"`
}
