package cloud

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/google/uuid"
	"github.com/zde37/pinata-go-sdk/pinata"
)

type PinataCloud struct {
	client     *pinata.Client
	gatewayURL string
}

var _ Cloud = (*PinataCloud)(nil)

func NewPinataCloud(jwt string, gatewayURL ...string) (*PinataCloud, error) {
	if jwt == "" {
		return nil, fmt.Errorf("pinata JWT cannot be empty")
	}

	auth := pinata.NewAuthWithJWT(jwt)
	client := pinata.New(auth)

	// ensure JWT is valid.
	if _, err := client.TestAuthentication(); err != nil {
		return nil, fmt.Errorf("pinata authentication failed: %w", err)
	}

	gwURL := "https://gateway.pinata.cloud"
	if len(gatewayURL) > 0 && gatewayURL[0] != "" {
		gwURL = gatewayURL[0]
	}

	return &PinataCloud{
		client:     client,
		gatewayURL: gwURL,
	}, nil
}

// Upload pins the given data to IPFS via Pinata and returns the content identifier (CID) as a Ref.
func (p *PinataCloud) Upload(data []byte, contentType ...string) (Ref, error) {
	// We must use the file pinning endpoint to upload raw bytes without modification.
	// We build the multipart request body in memory to avoid writing a temporary file.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Create a form file part with a random name.
	// Pinata doesn't use this filename for the CID.
	part, err := writer.CreateFormFile("file", uuid.New().String())
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	// Copy the raw byte data into the form file part.
	if _, err = io.Copy(part, bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf(
			"failed to copy data to multipart writer: %w",
			err,
		)
	}

	if err = writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// The Pinata SDK doesn't expose a method to pin raw bytes directly,
	// so we use the underlying requestBuilder to send the request.
	var response struct {
		IpfsHash string `json:"IpfsHash"`
	}

	err = p.client.NewRequest(http.MethodPost, "/pinning/pinFileToIPFS").
		SetBody(body, writer.FormDataContentType()).
		Send(&response)

	if err != nil {
		return "", fmt.Errorf("pinata API request failed: %w", err)
	}

	if response.IpfsHash == "" {
		return "", fmt.Errorf("pinata API response did not include an IpfsHash")
	}

	return Ref(response.IpfsHash), nil
}

// Read fetches the data from the IPFS gateway corresponding to the Ref (CID).
func (p *PinataCloud) Read(ref Ref) ([]byte, error) {
	if ref == "" {
		return nil, fmt.Errorf("ref (CID) cannot be empty")
	}

	url := fmt.Sprintf("%s/ipfs/%s", p.gatewayURL, string(ref))

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from IPFS gateway: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close response body: %v\n", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"IPFS gateway returned non-200 status: %s",
			resp.Status,
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}
