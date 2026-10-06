//go:build fuzz

package contexty

import (
	"context"
	"testing"
)

func FuzzCharFallbackEstimator(f *testing.F) {
	f.Add("hello world", 4)
	f.Add("", 1)
	f.Add("привет", 4)
	f.Add("ab", int(^uint(0)>>1))
	f.Fuzz(func(t *testing.T, text string, charsPerToken int) {
		if charsPerToken <= 0 {
			t.Skip()
		}
		c := &CharFallbackEstimator{CharsPerToken: charsPerToken}
		msgs := []Message{TextMessage(RoleUser, text)}
		n, err := c.Estimate(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if n < 0 {
			t.Fatalf("negative token count: %d", n)
		}
	})
}

func FuzzEstimateArithmetic(f *testing.F) {
	maximum := int(^uint(0) >> 1)
	for _, seed := range [][2]int{{0, 0}, {maximum, 0}, {maximum, 1}, {-1, 1}, {maximum / 2, maximum/2 + 1}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b int) {
		value, err := addEstimateTokens(a, b)
		invalid := a < 0 || b < 0 || uint64(a)+uint64(b) > uint64(maximum)
		if invalid && err == nil {
			t.Fatalf("invalid sum admitted: %d+%d=%d", a, b, value)
		}
		if !invalid && (err != nil || value != a+b) {
			t.Fatalf("valid sum failed: %d+%d=%d, %v", a, b, value, err)
		}
	})
}
