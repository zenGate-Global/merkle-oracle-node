package cloud

// Ref is an opaque reference to a blob stored in the cloud.
type Ref string

// Cloud defines a storage backend capable of uploading and reading binary data.
type Cloud interface {
	// Upload stores the given data and returns a reference pointing to it.
	// contentType is optional and defaults to "application/json" if not provided.
	// It returns an error if the upload fails.
	Upload(data []byte, contentType ...string) (Ref, error)

	// Read fetches the data identified by ref.
	// It returns the raw bytes or an error if the read fails.
	Read(ref Ref) ([]byte, error)
}
