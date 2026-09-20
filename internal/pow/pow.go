// Package pow implements the hashcash-style proof of work the browser must
// complete before it is issued a challenge or a token.
//
// The point is not to stop a determined attacker: a single solve costs a
// fraction of a second. The point is to make bulk abuse expensive. Solving a
// million captchas now costs a million CPU-seconds of client work on top of
// everything else, which changes the economics of farming without being
// noticeable to a real visitor.
package pow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/bits"
	"strings"
)

// Challenge is the puzzle handed to the browser.
type Challenge struct {
	// Algorithm is always "sha256-lz" today: SHA-256 with a leading-zero-bits
	// target. It is sent so the widget can refuse a scheme it does not know
	// rather than burn CPU on a wrong guess.
	Algorithm string `json:"alg"`
	// Prefix is the random string the nonce is appended to.
	Prefix string `json:"prefix"`
	// Bits is how many leading zero bits the digest must have.
	Bits int `json:"bits"`
}

// Algorithm is the only scheme this package implements.
const Algorithm = "sha256-lz"

// MaxBits caps the difficulty so a configuration mistake cannot lock every
// visitor out behind an unsolvable puzzle. At 24 bits a phone already needs
// several seconds.
const MaxBits = 24

// New creates a challenge with the given difficulty.
func New(difficultyBits int) (Challenge, error) {
	if difficultyBits < 1 {
		difficultyBits = 1
	}
	if difficultyBits > MaxBits {
		difficultyBits = MaxBits
	}
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return Challenge{}, fmt.Errorf("pow: %w", err)
	}
	return Challenge{
		Algorithm: Algorithm,
		Prefix:    hex.EncodeToString(buf[:]),
		Bits:      difficultyBits,
	}, nil
}

// Verify reports whether nonce solves the challenge.
func (c Challenge) Verify(nonce string) bool {
	if c.Algorithm != Algorithm || c.Prefix == "" {
		return false
	}
	// A nonce is a short token; anything longer is an attempt to make the
	// server hash a large buffer for free.
	if len(nonce) == 0 || len(nonce) > 64 || strings.ContainsAny(nonce, "\x00\n\r") {
		return false
	}
	sum := sha256.Sum256([]byte(c.Prefix + "." + nonce))
	return LeadingZeroBits(sum[:]) >= c.Bits
}

// LeadingZeroBits counts the zero bits at the start of a digest.
func LeadingZeroBits(digest []byte) int {
	n := 0
	for _, b := range digest {
		if b != 0 {
			return n + bits.LeadingZeros8(b)
		}
		n += 8
	}
	return n
}

// Solve finds a nonce for the challenge. The service itself never needs this,
// but the tests do, and it documents the algorithm the widget implements.
func Solve(c Challenge, maxIterations int) (string, bool) {
	for i := range maxIterations {
		nonce := fmt.Sprintf("%d", i)
		if c.Verify(nonce) {
			return nonce, true
		}
	}
	return "", false
}

// Difficulty maps a risk score in [0,1], where 1 is most suspicious, onto a
// number of bits. Trusted-looking visitors pay less.
func Difficulty(base int, risk float64) int {
	switch {
	case risk < 0:
		risk = 0
	case risk > 1:
		risk = 1
	}
	return min(base+int(risk*6), MaxBits)
}
