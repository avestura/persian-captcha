package pow

import (
	"crypto/sha256"
	"testing"
)

func TestSolveAndVerify(t *testing.T) {
	for _, bits := range []int{4, 8, 12} {
		c, err := New(bits)
		if err != nil {
			t.Fatalf("New(%d): %v", bits, err)
		}
		nonce, ok := Solve(c, 5_000_000)
		if !ok {
			t.Fatalf("no solution found for %d bits", bits)
		}
		if !c.Verify(nonce) {
			t.Errorf("solution %q rejected at %d bits", nonce, bits)
		}
		sum := sha256.Sum256([]byte(c.Prefix + "." + nonce))
		if got := LeadingZeroBits(sum[:]); got < bits {
			t.Errorf("solution has %d leading zero bits, want at least %d", got, bits)
		}
	}
}

func TestVerifyRejectsJunk(t *testing.T) {
	c, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		challenge Challenge
		nonce     string
	}{
		"empty nonce":      {c, ""},
		"wrong nonce":      {c, "definitely-not-a-solution"},
		"oversized nonce":  {c, string(make([]byte, 65))},
		"embedded newline": {c, "12\n34"},
		"wrong algorithm":  {Challenge{Algorithm: "scrypt", Prefix: c.Prefix, Bits: 8}, "0"},
		"empty prefix":     {Challenge{Algorithm: Algorithm, Prefix: "", Bits: 8}, "0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.challenge.Verify(tc.nonce) {
				t.Error("accepted a nonce it should have rejected")
			}
		})
	}
}

func TestLeadingZeroBits(t *testing.T) {
	cases := []struct {
		digest []byte
		want   int
	}{
		{[]byte{0xff}, 0},
		{[]byte{0x7f}, 1},
		{[]byte{0x01}, 7},
		{[]byte{0x00, 0x80}, 8},
		{[]byte{0x00, 0x00, 0x20}, 18},
		{[]byte{0x00, 0x00}, 16},
	}
	for _, tc := range cases {
		if got := LeadingZeroBits(tc.digest); got != tc.want {
			t.Errorf("LeadingZeroBits(%x) = %d, want %d", tc.digest, got, tc.want)
		}
	}
}

func TestDifficultyScalesWithRiskAndIsCapped(t *testing.T) {
	if trusted, suspicious := Difficulty(16, 0), Difficulty(16, 1); trusted >= suspicious {
		t.Errorf("a suspicious visitor should pay more: %d vs %d", suspicious, trusted)
	}
	if got := Difficulty(MaxBits, 1); got != MaxBits {
		t.Errorf("Difficulty exceeded the cap: got %d, want %d", got, MaxBits)
	}
	// Out-of-range risks must not produce a nonsensical difficulty.
	if got := Difficulty(16, -5); got != 16 {
		t.Errorf("negative risk changed the difficulty: got %d", got)
	}
}

func TestNewClampsDifficulty(t *testing.T) {
	high, err := New(1000)
	if err != nil {
		t.Fatal(err)
	}
	if high.Bits != MaxBits {
		t.Errorf("Bits = %d, want the cap %d", high.Bits, MaxBits)
	}
	low, err := New(-3)
	if err != nil {
		t.Fatal(err)
	}
	if low.Bits != 1 {
		t.Errorf("Bits = %d, want 1", low.Bits)
	}
}

func TestPrefixesAreUnique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for range 100 {
		c, err := New(4)
		if err != nil {
			t.Fatal(err)
		}
		if seen[c.Prefix] {
			t.Fatalf("prefix %q was issued twice", c.Prefix)
		}
		seen[c.Prefix] = true
	}
}
