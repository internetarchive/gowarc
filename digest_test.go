package warc

import (
	"bytes"
	"strings"
	"testing"
)

// Tests for the GetSHA1 function
func TestGetSHA1(t *testing.T) {
	helloWorldSHA1 := "sha1:FKXGYNOJJ7H3IFO35FPUBC445EPOQRXN"

	hash, err := GetDigest(bytes.NewReader([]byte("hello world")), SHA1)
	if err != nil {
		t.Errorf("Failed to generate SHA1: %v", err)
	}

	if hash != helloWorldSHA1 {
		t.Errorf("Failed to generate SHA1, expected %s, got %s", helloWorldSHA1, hash)
	}
}

func TestSHA1EmptyBytes(t *testing.T) {
	// Create an empty byte slice reader
	emptyBytes := make([]byte, 0)
	emptyReader := strings.NewReader(string(emptyBytes))

	// Generate SHA1 hash
	hash, err := GetDigest(emptyReader, SHA1)
	if err != nil {
		t.Fatalf("failed to get SHA1 digest of empty bytes: %v", err)
	}

	expectedHash := "sha1:3I42H3S6NNFQ2MSVX7XZKYAYSCX5QBYJ"

	if hash != expectedHash {
		t.Fatalf("expected SHA1 hash %s, got %s", expectedHash, hash)
	}

	t.Logf("SHA1 hash of empty bytes: %s", hash)
}

// Tests for the GetSHA256Base32 function
func TestGetSHA256Base32(t *testing.T) {
	helloWorldSHA256Base32 := "sha256:XFGSPOMTJU7ARJJOKLL5U7NL7LCIJ37DPJJYB3UQRD32ZYXPZXUQ===="

	hash, err := GetDigest(bytes.NewReader([]byte("hello world")), SHA256Base32)
	if err != nil {
		t.Errorf("Failed to generate SHA256Base32: %v", err)
		return
	}

	if hash != helloWorldSHA256Base32 {
		t.Errorf("Failed to generate SHA256Base32, expected %s, got %s", helloWorldSHA256Base32, hash)
		return
	}
}

func TestSHA256Base32EmptyBytes(t *testing.T) {
	// Create an empty byte slice reader
	emptyBytes := make([]byte, 0)
	emptyReader := strings.NewReader(string(emptyBytes))

	// Generate SHA256Base32 hash
	hash, err := GetDigest(emptyReader, SHA256Base32)
	if err != nil {
		t.Fatalf("failed to get SHA256Base32 digest of empty bytes: %v", err)
	}

	expectedHash := "sha256:4OYMIQUY7QOBJGX36TEJS35ZEQT24QPEMSNZGTFESWMRW6CSXBKQ===="

	if hash != expectedHash {
		t.Fatalf("expected SHA256Base32 hash %s, got %s", expectedHash, hash)
	}

	t.Logf("SHA256Base32 hash of empty bytes: %s", hash)
}

// Tests for the GetSHA256Base16 function
func TestGetSHA256Base16(t *testing.T) {
	helloWorldSHA256Base16 := "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	hash, err := GetDigest(bytes.NewReader([]byte("hello world")), SHA256Base16)
	if err != nil {
		t.Errorf("Failed to generate SHA256Base16: %v", err)
		return
	}

	if hash != helloWorldSHA256Base16 {
		t.Errorf("Failed to generate SHA256Base16, expected %s, got %s", helloWorldSHA256Base16, hash)
		return
	}
}

func TestSHA256Base16EmptyBytes(t *testing.T) {
	// Create an empty byte slice reader
	emptyBytes := make([]byte, 0)
	emptyReader := strings.NewReader(string(emptyBytes))

	// Generate SHA256Base16 hash
	hash, err := GetDigest(emptyReader, SHA256Base16)
	if err != nil {
		t.Fatalf("failed to get SHA256Base16 digest of empty bytes: %v", err)
	}

	expectedHash := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	if hash != expectedHash {
		t.Fatalf("expected SHA256Base16 hash %s, got %s", expectedHash, hash)
	}

	t.Logf("SHA256Base16 hash of empty bytes: %s", hash)
}

func TestGetBLAKE3(t *testing.T) {
	helloWorldBLAKE3 := "blake3:d74981efa70a0c880b8d8c1985d075dbcbf679b99a5f9914e5aaf96b831a9e24"

	hash, err := GetDigest(bytes.NewReader([]byte("hello world")), BLAKE3)
	if err != nil {
		t.Errorf("Failed to generate BLAKE3: %v", err)
		return
	}

	if hash != helloWorldBLAKE3 {
		t.Errorf("Failed to generate BLAKE3, expected %s, got %s", helloWorldBLAKE3, hash)
		return
	}
}

func TestBlake3EmptyBytes(t *testing.T) {
	// Create an empty byte slice reader
	emptyBytes := make([]byte, 0)
	emptyReader := strings.NewReader(string(emptyBytes))

	// Generate BLAKE3 hash
	hash, err := GetDigest(emptyReader, BLAKE3)
	if err != nil {
		t.Fatalf("failed to get BLAKE3 digest of empty bytes: %v", err)
	}

	expectedHash := "blake3:af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"

	if hash != expectedHash {
		t.Fatalf("expected BLAKE3 hash %s, got %s", expectedHash, hash)
	}

	t.Logf("BLAKE3 hash of empty bytes: %s", hash)
}
