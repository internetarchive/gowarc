package warc

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
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

// Map of supported algorithms to their corresponding digest functions
// digestFunctions := map[string]warc.DigestAlgorithm{
// 	"sha1":   warc.SHA1,
// 	"sha256": warc.SHA256Base16,
// 	"blake3": warc.BLAKE3,
// }

func IsDigestSupported(algorithm string) bool {
	switch algorithm {
	case "sha1", "sha-1", "sha256", "sha-256", "blake3":
		return true
	default:
		return false
	}
}

func GetDigestFromPrefix(prefix string) DigestAlgorithm {
	switch prefix {
	case "sha1", "sha-1":
		return SHA1
	case "sha256", "sha-256":
		return SHA256Base16
	case "blake3":
		return BLAKE3
	default:
		return -1
	}
}

func GetDigest(r io.Reader, digestAlgorithm DigestAlgorithm) (string, error) {
	switch digestAlgorithm {
	case SHA1:
		return getSHA1(r)
	case SHA256Base16:
		return getSHA256Base16(r)
	case SHA256Base32:
		return getSHA256Base32(r)
	case BLAKE3:
		return getBLAKE3(r)
	default:
		return "", ErrUnknownDigestAlgorithm
	}
}

func getSHA1(r io.Reader) (string, error) {
	sha := sha1.New()

	_, err := io.Copy(sha, r)
	if err != nil {
		return "", err
	}

	return "sha1:" + base32.StdEncoding.EncodeToString(sha.Sum(nil)), nil
}

func getSHA256Base32(r io.Reader) (string, error) {
	sha := sha256.New()

	_, err := io.Copy(sha, r)
	if err != nil {
		return "", err
	}

	return "sha256:" + base32.StdEncoding.EncodeToString(sha.Sum(nil)), nil
}

func getSHA256Base16(r io.Reader) (string, error) {
	sha := sha256.New()

	_, err := io.Copy(sha, r)
	if err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(sha.Sum(nil)), nil
}

func getBLAKE3(r io.Reader) (string, error) {
	hash := blake3.New()

	_, err := io.Copy(hash, r)
	if err != nil {
		return "", err
	}

	return "blake3:" + hex.EncodeToString(hash.Sum(nil)), nil
}
