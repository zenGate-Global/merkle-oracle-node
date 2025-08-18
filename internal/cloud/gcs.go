// cloud/gcs.go
package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"google.golang.org/api/option"
)

type GCSBucket struct {
	client *storage.Client
	bucket string
}

func NewGCSBucket(
	ctx context.Context,
	bucketName string,
	keyPath string,
) (*GCSBucket, error) {
	client, err := storage.NewClient(ctx, option.WithCredentialsFile(keyPath))
	if err != nil {
		return nil, fmt.Errorf("storage.NewClient: %w", err)
	}
	return &GCSBucket{client: client, bucket: bucketName}, nil
}

// Upload writes data to GCS under a generated object name and returns that name as a Ref.
// contentType is optional and defaults to "application/json" if not provided.
func (g *GCSBucket) Upload(data []byte, contentType ...string) (Ref, error) {
	name := time.Now().
		UTC().
		Format("20060102-150405") +
		"-" + uuid.New().
		String()
	wc := g.client.Bucket(g.bucket).Object(name).NewWriter(context.Background())

	if len(contentType) > 0 && contentType[0] != "" {
		wc.ContentType = contentType[0]
	} else {
		wc.ContentType = "application/json"
	}

	if _, err := wc.Write(data); err != nil {
		return "", errors.Join(fmt.Errorf("writer.Write: %w", err), wc.Close())
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("writer.Close: %w", err)
	}
	return Ref(name), nil
}

// Read fetches the bytes stored under that Ref (object name).
func (g *GCSBucket) Read(ref Ref) (_ []byte, err error) {
	rc, err := g.client.Bucket(g.bucket).
		Object(string(ref)).
		NewReader(context.Background())
	if err != nil {
		return nil, fmt.Errorf("object.NewReader: %w", err)
	}
	defer func() {
		err = errors.Join(err, rc.Close())
	}()

	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("io.ReadAll: %w", err)
	}
	return b, nil
}
