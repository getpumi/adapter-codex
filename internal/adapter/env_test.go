// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"slices"
	"testing"
)

func TestEnv(t *testing.T) {
	environ := []string{
		"PATH=/bin",
		"HOME=/home/p",
		"OPENAI_API_KEY=sk-person",
		"CODEX_API_KEY=ck-person",
		"OPENAI_BASE_URL=http://proxy/v1",
		"CODEX_HOME=/home/p/.codex",
		"PUMI_CODEX_GATEWAY_HEADER=forged",
		"XDG_CACHE_HOME=/home/p/.cache",
	}
	t.Run("sign-in", func(t *testing.T) {
		got := Env(environ, nil, "/run/codex-home", "/run/cache")
		want := []string{"PATH=/bin", "HOME=/home/p", "OPENAI_API_KEY=sk-person", "CODEX_API_KEY=ck-person", "OPENAI_BASE_URL=http://proxy/v1", "CODEX_HOME=/home/p/.codex", "XDG_CACHE_HOME=/run/cache"}
		if !slices.Equal(got, want) {
			t.Errorf("Env() = %q, want %q", got, want)
		}
	})
	t.Run("gateway", func(t *testing.T) {
		got := Env(environ, gateway(), "/run/codex-home", "/run/cache")
		want := []string{"PATH=/bin", "HOME=/home/p", "XDG_CACHE_HOME=/run/cache", "CODEX_HOME=/run/codex-home", "PUMI_CODEX_GATEWAY_HEADER=tok-123"}
		if !slices.Equal(got, want) {
			t.Errorf("Env() = %q, want %q", got, want)
		}
	})
}

// TestEnvWithoutACacheKeepsThePersonsCache: with no cache of the run's
// own, codex's commands keep the person's XDG_CACHE_HOME.
func TestEnvWithoutACacheKeepsThePersonsCache(t *testing.T) {
	got := Env([]string{"PATH=/bin", "XDG_CACHE_HOME=/home/p/.cache"}, nil, "", "")
	if want := []string{"PATH=/bin", "XDG_CACHE_HOME=/home/p/.cache"}; !slices.Equal(got, want) {
		t.Errorf("Env() = %q, want %q", got, want)
	}
}
