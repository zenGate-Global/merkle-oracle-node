package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	connector "github.com/zenGate-Global/cardano-connector-go"
)

func SubmitTx(
	cfg *config.Config,
	provider connector.Provider,
	txBytes []byte,
) (string, error) {
	if cfg.Submit.Url != "" {
		req, err := http.NewRequestWithContext(
			context.Background(),
			"POST",
			cfg.Submit.Url,
			bytes.NewReader(txBytes),
		)
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/cbor")

		if cfg.Submit.BlockFrostProjectID != "" {
			req.Header.Set("project_id", cfg.Submit.BlockFrostProjectID)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("request failed: %w", err)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				logging.GetLogger().
					Warnf("failed to close response body: %v", closeErr)
			}
		}()

		respBodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read response body: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf(
				"submission failed: status %d - %s. Body: %s",
				resp.StatusCode,
				http.StatusText(resp.StatusCode),
				string(respBodyBytes),
			)
		}

		txHash := strings.Trim(string(respBodyBytes), "\"")
		if txHash == "" {
			return "", fmt.Errorf(
				"custom endpoint did not return a transaction hash",
			)
		}
		return txHash, nil
	}

	txHash, err := provider.SubmitTx(context.Background(), txBytes)
	if err != nil {
		return "", err
	}
	return txHash, nil
}
