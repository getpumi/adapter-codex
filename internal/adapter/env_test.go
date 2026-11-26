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
	}
	t.Run("sign-in", func(t *testing.T) {
		got := Env(environ, nil, "/run/codex-home")
		want := []string{"PATH=/bin", "HOME=/home/p", "OPENAI_API_KEY=sk-person", "CODEX_API_KEY=ck-person", "OPENAI_BASE_URL=http://proxy/v1", "CODEX_HOME=/home/p/.codex"}
		if !slices.Equal(got, want) {
			t.Errorf("Env() = %q, want %q", got, want)
		}
	})
	t.Run("gateway", func(t *testing.T) {
		got := Env(environ, gateway(), "/run/codex-home")
		want := []string{"PATH=/bin", "HOME=/home/p", "CODEX_HOME=/run/codex-home", "PUMI_CODEX_GATEWAY_HEADER=tok-123"}
		if !slices.Equal(got, want) {
			t.Errorf("Env() = %q, want %q", got, want)
		}
	})
}
