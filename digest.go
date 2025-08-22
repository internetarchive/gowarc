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
	SHA256Base16
	SHA256Base32
	BLAKE3
)

var ErrUnknownDigestAlgorithm = errors.New("unknown digest algorithm")

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
