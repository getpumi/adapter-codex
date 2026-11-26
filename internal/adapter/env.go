// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"slices"
	"strings"

	harnessv1 "getpumi.dev/pumi/api/gen/pumi/harness/v1"
)

// credentials are the variables of the person's own OpenAI credentials and
// provider settings, which codex would send with its calls or use to pick
// where they go. The adapter clears them on the API key path, where codex
// sends only the invocation token, to the gateway (decisions 0226 and
// 0240). The names are from codex 0.172.0's binary.
var credentials = []string{
	"OPENAI_API_KEY",
	"CODEX_API_KEY",
	"CODEX_ACCESS_TOKEN",
	"OPENAI_BASE_URL",
	"OPENAI_ORGANIZATION",
	"OPENAI_PROJECT",
	"CODEX_HOME",
	"CODEX_SQLITE_HOME",
}

// cacheVariable names the base directory of the caches of commands, such as
// Go's build cache, in the XDG Base Directory Specification.
const cacheVariable = "XDG_CACHE_HOME"

// Env returns codex's environment: the adapter's environment without the
// variable that carries the invocation token, which only the adapter sets.
// With cache, the run's own cache directory, it points XDG_CACHE_HOME
// there, so that commands in codex's sandbox write their caches there and
// not to the person's (decision 0269).
//
// With a gateway, it also leaves out the person's own credentials and
// provider settings, gives codex a home of its own, codexHome, so that it
// reads no sign-in, and sets the token for the gateway's header.
//
// Without a gateway, on the sign-in path, codex keeps the person's own
// home, sign-in and provider settings and talks to OpenAI directly
// (decision 0240).
func Env(environ []string, gw *harnessv1.Gateway, codexHome, cache string) []string {
	cleared := []string{GatewayHeaderVariable}
	if gw != nil {
		cleared = append(cleared, credentials...)
	}
	if cache != "" {
		cleared = append(cleared, cacheVariable)
	}
	env := make([]string, 0, len(environ)+3)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(cleared, name) {
			env = append(env, kv)
		}
	}
	if cache != "" {
		env = append(env, cacheVariable+"="+cache)
	}
	if gw == nil {
		return env
	}
	return append(env, "CODEX_HOME="+codexHome, GatewayHeaderVariable+"="+gw.GetToken())
}
