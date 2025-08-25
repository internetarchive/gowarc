package warc

import (
	"bytes"
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
