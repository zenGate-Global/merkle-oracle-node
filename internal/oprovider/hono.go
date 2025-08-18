// hono.go
package oprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type HonoProvider struct {
	BaseURL string
	Client  *http.Client
}

func NewHonoProvider(baseURL string) *HonoProvider {
	return &HonoProvider{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client:  http.DefaultClient,
	}
}

func (h *HonoProvider) Fetch(
	ctx context.Context,
	endpoint string,
) (_ []Item, err error) {
	ep := strings.TrimPrefix(endpoint, "/")
	url := fmt.Sprintf("%s/%s", h.BaseURL, ep)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("performing GET %s: %w", url, err)
	}
	defer func() {
		err = errors.Join(err, resp.Body.Close())
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"unexpected status %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	var payload struct {
		Count     int                      `json:"count"`
		Timestamp string                   `json:"timestamp"`
		Data      []map[string]interface{} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding JSON: %w", err)
	}

	items := make([]Item, len(payload.Data))
	for i, rec := range payload.Data {
		m := make(Item, len(rec))
		for k, v := range rec {
			m[k] = fmt.Sprint(v)
		}
		items[i] = m
	}

	return items, nil
}
