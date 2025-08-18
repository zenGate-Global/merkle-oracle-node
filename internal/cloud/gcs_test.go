package cloud

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

const (
	bucketName  = "oracle-bucket-dev"
	keyPath     = "../../gcp.json"
	dataPath    = "../../data.json"
	uploadedRef = "20250730-065914-24e8c54b-589e-43a5-b8e6-610b6ab4badb"
)

func TestGCSBucket_Upload(t *testing.T) {
	// Check if required files exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Skip("path not found, skipping GCS integration test")
	}
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("path not found, skipping GCS integration test")
	}

	// Read test data
	data, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("Failed to read data path: %v", err)
	}

	// Create GCS bucket client
	ctx := context.Background()
	gcs, err := NewGCSBucket(ctx, bucketName, keyPath)
	if err != nil {
		t.Fatalf("Failed to create GCS bucket: %v", err)
	}

	ref, err := gcs.Upload(data)
	if err != nil {
		t.Fatalf("Failed to upload data: %v", err)
	}

	if ref == "" {
		t.Error("Expected non-empty reference, got empty string")
	}

	ref2, err := gcs.Upload(data, "application/json")
	if err != nil {
		t.Fatalf("Failed to upload data with custom content type: %v", err)
	}

	if ref2 == "" {
		t.Error("Expected non-empty reference, got empty string")
	}

	if ref == ref2 {
		t.Error("Expected different references for different uploads")
	}

	t.Logf("Successfully uploaded files with refs: %s, %s", ref, ref2)
}

func TestGCSBucket_Read(t *testing.T) {
	// Check if required files exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Skip("gcp.json not found, skipping GCS integration test")
	}
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Skip("data.json not found, skipping GCS integration test")
	}

	// Read test data
	originalData, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("Failed to read data path: %v", err)
	}

	// Create GCS bucket client
	ctx := context.Background()
	gcs, err := NewGCSBucket(ctx, bucketName, keyPath)
	if err != nil {
		t.Fatalf("Failed to create GCS bucket: %v", err)
	}

	readData, err := gcs.Read(uploadedRef)
	if err != nil {
		t.Fatalf("Failed to read data by ref: %v", err)
	}

	originalHash := sha256.Sum256(originalData)
	readHash := sha256.Sum256(readData)

	originalHashHex := fmt.Sprintf("%x", originalHash)
	readHashHex := fmt.Sprintf("%x", readHash)

	if originalHashHex != readHashHex {
		t.Errorf(
			"SHA256 hashes do not match!\nOriginal: %s\nRead:     %s",
			originalHashHex,
			readHashHex,
		)
	} else {
		t.Logf("SHA256 hashes match: %s", originalHashHex)
	}
}
