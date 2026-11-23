package imperative

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
)

// ImperativeHeader is the request header that carries the JSON-encoded [Imperative].
const ImperativeHeader = "X-Echo-Imperative"

// Parses a Imperative from a JSON string
func ParseImperative(s string) (Imperative, error) {
	var d Imperative
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return d, err
	}
	return d, nil
}

// Encodes a Imperative to JSON
func (imp Imperative) Encode() string {
	b, _ := json.Marshal(imp)
	return string(b)
}

// Hashes the Imperative
func (imp Imperative) Hash() string {
	hash := sha256.New()
	hash.Write([]byte(imp.Encode()))
	return hex.EncodeToString(hash.Sum(nil))
}

// Multiply the Imperative by n times
func (imp Imperative) Multiply(n int) Imperatives {
	out := make(Imperatives, n)
	for i := range out {
		out[i] = imp
	}
	return out
}

// Aborts returns true if the Imperative is aborted
func (imp Imperative) Aborts() bool {
	return imp.Transport.Abort
}

// Validate a single Imperative
func (imp Imperative) Validate() error {
	if imp.Response.StatusCode == 0 {
		return fmt.Errorf("imperative: response status code is required")
	}
	if err := imp.Response.Body.Validate(); err != nil {
		return err
	}
	if err := imp.Request.Body.Validate(); err != nil {
		return err
	}
	return nil
}

// Imperatives is a slice of [Imperative] values
type Imperatives []Imperative

// Fills the Seed field with random positive numbers
func (imps Imperatives) Seed(baseSeed uint64) Imperatives {
	out := make(Imperatives, len(imps))
	copy(out, imps)
	for i := range out {
		r := rand.New(rand.NewPCG(baseSeed, uint64(i)))
		if out[i].Response.Body.Length > 0 {
			out[i].Response.Body.Seed = r.IntN(1_000_000_000)
		}
		if out[i].Request.Body.Length > 0 {
			out[i].Request.Body.Seed = r.IntN(1_000_000_000)
		}
	}
	return out
}

// Randomly shuffles the imperatives in place
func (imps Imperatives) Shuffle(seed uint64) Imperatives {
	out := make(Imperatives, len(imps))
	copy(out, imps)

	// PCG requires two seeds, but we just set the stream selector to 0
	src := rand.NewPCG(seed, 0)
	r := rand.New(src)

	r.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})

	return out
}

// Hash all imperatives and return a map of hash -> []Imperative
func (imps Imperatives) Hash() map[string][]Imperative {
	out := make(map[string][]Imperative)
	for _, imp := range imps {
		h := imp.Hash()
		out[h] = append(out[h], imp)
	}
	return out
}

// Multiply all imperatives by n times
func (imps Imperatives) Multiply(n int) Imperatives {
	out := make(Imperatives, 0, len(imps)*n)
	for _, imp := range imps {
		for i := 0; i < n; i++ {
			out = append(out, imp)
		}
	}
	return out
}

// Validate all imperatives
func (imps Imperatives) Validate() error {
	for _, imp := range imps {
		if err := imp.Validate(); err != nil {
			return err
		}
	}
	return nil
}
