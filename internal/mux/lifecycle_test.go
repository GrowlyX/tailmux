package mux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCloseWithoutSuccessfulStart(t *testing.T) {
	for _, start := range []bool{false, true} {
		name := "before start"
		if start {
			name = "failed start"
		}
		t.Run(name, func(t *testing.T) {
			blocked := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(blocked, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{StateDir: blocked, Tailnets: []TailnetConfig{{Name: "alpha"}}}
			if err := cfg.Normalize(); err != nil {
				t.Fatal(err)
			}
			m := New(cfg, Options{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := func() error {
				// Match startup's deferred cleanup: it must preserve the
				// original filesystem error, not replace it with a panic.
				defer func() {
					if err := m.Close(); err != nil {
						t.Errorf("Close: %v", err)
					}
				}()
				if start {
					return m.Start(ctx)
				}
				return nil
			}()
			if start {
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) {
					t.Fatalf("Start = %v, want the state directory error", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
