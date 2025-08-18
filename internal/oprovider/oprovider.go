package oprovider

import "context"

// Item represents a single record as a map of string keys to string values.
type Item map[string]string

// Provider is the common interface for any oprovider implementation.
// Fetch should contact the given API endpoint and return the response
// as a slice of Items, ensuring all values are string‑typed.
type Provider interface {
	Fetch(ctx context.Context, endpoint string) ([]Item, error)
}
