package warc

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"hash"
	"io"

	"github.com/zeebo/blake3"
)

type DigestAlgorithm int

const (
	SHA1 DigestAlgorithm = iota
	// According to IIPC, lowercase base 16 is the "popular" encoding for SHA256
	SHA256Base16
	SHA256Base32
	BLAKE3
)

var ErrUnknownDigestAlgorithm = errors.New("unknown digest algorithm")

func IsDigestSupported(algorithm string) bool {
	switch algorithm {
	case "sha1", "sha256", "blake3":
		return true
	default:
		return false
	}
}

func GetDigestFromPrefix(prefix string) DigestAlgorithm {
	switch prefix {
	case "sha1":
		return SHA1
	case "sha256":
		return SHA256Base16
	case "blake3":
		return BLAKE3
	default:
		return -1
	}
}

func GetDigest(r io.Reader, digestAlgorithm DigestAlgorithm) (string, error) {
	var (
		hashFunc     func() any
		prefix       string
		encodingFunc func([]byte) string
	)

	switch digestAlgorithm {
	case SHA1:
		hashFunc = func() any { return sha1.New() }
		prefix = "sha1:"
		encodingFunc = func(b []byte) string {
			return base32.StdEncoding.EncodeToString(b)
		}
	case SHA256Base16:
		hashFunc = func() any { return sha256.New() }
		prefix = "sha256:"
		encodingFunc = hex.EncodeToString
	case SHA256Base32:
		hashFunc = func() any { return sha256.New() }
		prefix = "sha256:"
		encodingFunc = func(b []byte) string {
			return base32.StdEncoding.EncodeToString(b)
		}
	case BLAKE3:
		hashFunc = func() any { return blake3.New() }
		prefix = "blake3:"
		encodingFunc = hex.EncodeToString
	default:
		return "", ErrUnknownDigestAlgorithm
	}

	hasher := hashFunc()
	_, err := io.Copy(hasher.(io.Writer), r)
	if err != nil {
		return "", err
	}

	return prefix + encodingFunc(hasher.(hash.Hash).Sum(nil)), nil
}
