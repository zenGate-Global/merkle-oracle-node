package oprovider

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	serverURL = "http://localhost:3000"
)

func TestNewHonoProvider(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{
			name:     "URL without trailing slash",
			baseURL:  "https://api.example.com",
			expected: "https://api.example.com",
		},
		{
			name:     "URL with trailing slash",
			baseURL:  "https://api.example.com/",
			expected: "https://api.example.com",
		},
		{
			name:     "URL with multiple trailing slashes",
			baseURL:  "https://api.example.com///",
			expected: "https://api.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewHonoProvider(tt.baseURL)

			if provider.BaseURL != tt.expected {
				t.Errorf(
					"NewHonoProvider() BaseURL = %v, want %v",
					provider.BaseURL,
					tt.expected,
				)
			}

			if provider.Client != http.DefaultClient {
				t.Errorf(
					"NewHonoProvider() Client should be http.DefaultClient",
				)
			}
		})
	}
}

func TestHonoProvider_Fetch_Success(t *testing.T) {
	// Create test data
	testData := map[string]interface{}{
		"count":     2,
		"timestamp": "2024-01-01T00:00:00Z",
		"data": []map[string]interface{}{
			{
				"id":     123,
				"name":   "test1",
				"value":  45.67,
				"active": true,
			},
			{
				"id":     456,
				"name":   "test2",
				"value":  89.12,
				"active": false,
			},
		},
	}

	// Create test server
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("Expected GET request, got %s", r.Method)
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(testData); err != nil {
				t.Fatalf("Failed to encode test data: %v", err)
			}
		}),
	)
	defer server.Close()

	provider := NewHonoProvider(server.URL)

	items, err := provider.Fetch(context.Background(), "/test/endpoint")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if len(items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(items))
	}

	expected1 := Item{
		"id":     "123",
		"name":   "test1",
		"value":  "45.67",
		"active": "true",
	}
	if !equalItems(items[0], expected1) {
		t.Errorf("First item = %v, want %v", items[0], expected1)
	}

	expected2 := Item{
		"id":     "456",
		"name":   "test2",
		"value":  "89.12",
		"active": "false",
	}
	if !equalItems(items[1], expected2) {
		t.Errorf("Second item = %v, want %v", items[1], expected2)
	}
}

func TestHonoProvider_Fetch_EndpointFormats(t *testing.T) {
	requestedPath := ""
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestedPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"count":     0,
				"timestamp": "2024-01-01T00:00:00Z",
				"data":      []map[string]interface{}{},
			}); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		}),
	)
	defer server.Close()

	provider := NewHonoProvider(server.URL)

	tests := []struct {
		name         string
		endpoint     string
		expectedPath string
	}{
		{
			name:         "endpoint with leading slash",
			endpoint:     "/api/data",
			expectedPath: "/api/data",
		},
		{
			name:         "endpoint without leading slash",
			endpoint:     "api/data",
			expectedPath: "/api/data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := provider.Fetch(context.Background(), tt.endpoint)
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}

			if requestedPath != tt.expectedPath {
				t.Errorf(
					"Expected path %s, got %s",
					tt.expectedPath,
					requestedPath,
				)
			}
		})
	}
}

func TestHonoProvider_Fetch_HTTPErrors(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   string
		expectedErrMsg string
	}{
		{
			name:           "404 Not Found",
			statusCode:     http.StatusNotFound,
			responseBody:   "Not Found",
			expectedErrMsg: "unexpected status 404: Not Found",
		},
		{
			name:           "500 Internal Server Error",
			statusCode:     http.StatusInternalServerError,
			responseBody:   "Internal Server Error",
			expectedErrMsg: "unexpected status 500: Internal Server Error",
		},
		{
			name:           "400 Bad Request",
			statusCode:     http.StatusBadRequest,
			responseBody:   "Bad Request",
			expectedErrMsg: "unexpected status 400: Bad Request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.statusCode)
					if _, err := w.Write([]byte(tt.responseBody)); err != nil {
						t.Fatalf("Failed to write response body: %v", err)
					}
				}),
			)
			defer server.Close()

			provider := NewHonoProvider(server.URL)

			_, err := provider.Fetch(context.Background(), "/test")
			if err == nil {
				t.Fatal("Expected error but got none")
			}

			if !strings.Contains(err.Error(), tt.expectedErrMsg) {
				t.Errorf(
					"Expected error containing %q, got %q",
					tt.expectedErrMsg,
					err.Error(),
				)
			}
		})
	}
}

func TestHonoProvider_Fetch_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte("invalid json")); err != nil {
				t.Fatalf("Failed to write response body: %v", err)
			}
		}),
	)
	defer server.Close()

	provider := NewHonoProvider(server.URL)

	_, err := provider.Fetch(context.Background(), "/test")
	if err == nil {
		t.Fatal("Expected error but got none")
	}

	if !strings.Contains(err.Error(), "decoding JSON") {
		t.Errorf("Expected JSON decoding error, got %q", err.Error())
	}
}

func TestHonoProvider_Fetch_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// slow response
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"count":     0,
				"timestamp": "2024-01-01T00:00:00Z",
				"data":      []map[string]interface{}{},
			}); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		}),
	)
	defer server.Close()

	provider := NewHonoProvider(server.URL)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		50*time.Millisecond,
	)
	defer cancel()

	_, err := provider.Fetch(ctx, "/test")
	if err == nil {
		t.Fatal("Expected context cancellation error but got none")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf(
			"Expected context deadline exceeded error, got %q",
			err.Error(),
		)
	}
}

func TestHonoProvider_Fetch_NetworkError(t *testing.T) {
	provider := NewHonoProvider("http://non-existent-server:12345")

	_, err := provider.Fetch(context.Background(), "/test")
	if err == nil {
		t.Fatal("Expected network error but got none")
	}

	if !strings.Contains(err.Error(), "performing GET") {
		t.Errorf("Expected network error, got %q", err.Error())
	}
}

func TestHonoProvider_Fetch_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]interface{}{
				"count":     0,
				"timestamp": "2024-01-01T00:00:00Z",
				"data":      []map[string]interface{}{},
			}); err != nil {
				t.Fatal(err)
			}
		}),
	)
	defer server.Close()

	provider := NewHonoProvider(server.URL)

	items, err := provider.Fetch(context.Background(), "/test")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected 0 items, got %d", len(items))
	}
}

func TestHonoProvider_LiveServer(t *testing.T) {
	provider := NewHonoProvider(serverURL)

	// check if the server is reachable
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL, nil)
	if err != nil {
		t.Skipf("Failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("Live server not reachable at %s: %v", serverURL, err)
	}
	if err = resp.Body.Close(); err != nil {
		t.Fatalf("Failed to close response body: %v", err)
	}

	N := rand.Intn(6) + 5
	endpoint := strconv.Itoa(N)
	items, err := provider.Fetch(context.Background(), endpoint)
	if err != nil {
		t.Logf("Fetch error: %v", err)
		t.Skip("Skipping due to fetch error")
	}

	t.Logf("Fetched %d items from live server (expected %d)", len(items), N)

	if len(items) != N {
		t.Errorf("Expected %d items, but got %d", N, len(items))
	}
}

func equalItems(a, b Item) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
