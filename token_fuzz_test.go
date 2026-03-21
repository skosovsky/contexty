//go:build fuzz

package contexty

import (
	"context"
	"testing"
)

func FuzzCharFallbackCounter(f *testing.F) {
	f.Add("hello world", 4)
	f.Add("", 1)
	f.Add("привет", 4)
	f.Fuzz(func(t *testing.T, text string, charsPerToken int) {
		if charsPerToken <= 0 {
			t.Skip()
		}
		c := &CharFallbackCounter{CharsPerToken: charsPerToken}
		msgs := []Message{TextMessage(RoleUser, text)}
		n, err := c.Count(context.Background(), msgs)
		if err != nil {
			t.Fatal(err)
		}
		if n < 0 {
			t.Fatalf("negative token count: %d", n)
		}
	})
}
