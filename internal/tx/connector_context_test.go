package tx

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Salvionied/apollo/constants"
	blockfrostConnector "github.com/zenGate-Global/cardano-connector-go/blockfrost"
)

func setupCustomContext(t *testing.T) ConnectorContext {
	t.Helper()

	projectID := os.Getenv("BLOCKFROST_KEY")
	if projectID == "" {
		t.Log("BLOCKFROST_KEY environment variable not set")
	}

	config := blockfrostConnector.Config{
		ProjectID:   projectID,
		NetworkName: "preprod",
		NetworkId:   int(constants.PREPROD),
	}

	provider, err := blockfrostConnector.New(config)
	if err != nil {
		t.Fatalf("Failed to create Blockfrost provider: %v", err)
	}

	cc, err := NewConnectorContext(provider, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create ConnectorContext: %v", err)
	}

	return cc
}

func TestGetProtocolParameters(t *testing.T) {
	cc := setupCustomContext(t)
	start := time.Now()
	firstResp, err := cc.GetProtocolParams()
	firstRequestDuration := time.Since(start)
	if err != nil {
		t.Fatalf("Failed to get protocol parameters: %v", err)
	}

	t.Logf("First request duration: %v", firstRequestDuration)

	secondStart := time.Now()
	secondResp, err := cc.GetProtocolParams()
	if err != nil {
		t.Fatalf("Failed to get protocol parameters: %v", err)
	}
	secondRequestDuration := time.Since(secondStart)
	t.Logf("Second request duration: %v", secondRequestDuration)
	t.Logf(
		"Cache speedup: %.2fx",
		float64(firstRequestDuration)/float64(secondRequestDuration),
	)

	if !reflect.DeepEqual(firstResp, secondResp) {
		t.Fatalf("Cached response does not match first response")
	}
	t.Log("✓ Cached response matches first response")
}

func TestGetGenesisParams(t *testing.T) {
	cc := setupCustomContext(t)
	start := time.Now()
	firstResp, err := cc.GetGenesisParams()
	firstRequestDuration := time.Since(start)
	if err != nil {
		t.Fatalf("Failed to get genesis parameters: %v", err)
	}
	t.Logf("First request duration: %v", firstRequestDuration)

	secondStart := time.Now()
	secondResp, err := cc.GetGenesisParams()
	secondRequestDuration := time.Since(secondStart)
	if err != nil {
		t.Fatalf("Failed to get genesis parameters: %v", err)
	}
	t.Logf("Second request duration: %v", secondRequestDuration)
	t.Logf(
		"Cache speedup: %.2fx",
		float64(firstRequestDuration)/float64(secondRequestDuration),
	)

	if !reflect.DeepEqual(firstResp, secondResp) {
		t.Fatalf("Cached response does not match first response")
	}
	t.Log("✓ Cached response matches first response")
}

func TestEpoch(t *testing.T) {
	cc := setupCustomContext(t)
	start := time.Now()
	firstResp, err := cc.Epoch()
	firstRequestDuration := time.Since(start)
	if err != nil {
		t.Fatalf("Failed to get epoch: %v", err)
	}
	t.Logf("First request duration: %v", firstRequestDuration)

	secondStart := time.Now()
	secondResp, err := cc.Epoch()
	secondRequestDuration := time.Since(secondStart)
	if err != nil {
		t.Fatalf("Failed to get epoch: %v", err)
	}
	t.Logf("Second request duration: %v", secondRequestDuration)
	t.Logf(
		"Cache speedup: %.2fx",
		float64(firstRequestDuration)/float64(secondRequestDuration),
	)

	if !reflect.DeepEqual(firstResp, secondResp) {
		t.Fatalf("Cached response does not match first response")
	}
	t.Log("✓ Cached response matches first response")
}

func TestGetUtxosByOutRef(t *testing.T) {
	cc := setupCustomContext(t)
	start := time.Now()
	firstResp, err := cc.GetUtxoFromRef(
		"b50e73e74a3073bc44f555928702c0ae0f555a43f1afdce34b3294247dce022d",
		0,
	)
	firstRequestDuration := time.Since(start)
	if err != nil {
		t.Fatalf("Failed to get utxo: %v", err)
	}
	t.Logf("First request duration: %v", firstRequestDuration)

	secondStart := time.Now()
	secondResp, err := cc.GetUtxoFromRef(
		"b50e73e74a3073bc44f555928702c0ae0f555a43f1afdce34b3294247dce022d",
		0,
	)
	secondRequestDuration := time.Since(secondStart)
	if err != nil {
		t.Fatalf("Failed to get utxo: %v", err)
	}
	t.Logf("Second request duration: %v", secondRequestDuration)
	t.Logf(
		"Cache speedup: %.2fx",
		float64(firstRequestDuration)/float64(secondRequestDuration),
	)

	if !reflect.DeepEqual(firstResp, secondResp) {
		t.Fatalf("Cached response does not match first response")
	}
	t.Log("✓ Cached response matches first response")
}

func TestGetScriptCborByScriptHash(t *testing.T) {
	cc := setupCustomContext(t)
	start := time.Now()
	firstResp, err := cc.GetContractCbor(
		"fdb571c9117a6b54006f2dab0b431b0e257565b87bfcb0a067d62926",
	)
	firstRequestDuration := time.Since(start)
	if err != nil {
		t.Fatalf("Failed to get script cbor: %v", err)
	}
	t.Logf("First request duration: %v", firstRequestDuration)

	secondStart := time.Now()
	secondResp, err := cc.GetContractCbor(
		"fdb571c9117a6b54006f2dab0b431b0e257565b87bfcb0a067d62926",
	)
	secondRequestDuration := time.Since(secondStart)
	if err != nil {
		t.Fatalf("Failed to get script cbor on second call: %v", err)
	}
	t.Logf("Second request duration: %v", secondRequestDuration)
	t.Logf(
		"Cache speedup: %.2fx",
		float64(firstRequestDuration)/float64(secondRequestDuration),
	)

	if !reflect.DeepEqual(firstResp, secondResp) {
		t.Fatalf("Cached response does not match first response")
	}
	t.Log("✓ Cached response matches first response")
}
