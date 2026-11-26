<!--
SPDX-License-Identifier: Apache-2.0
SPDX-FileCopyrightText: The Pumi Authors
-->

# Pumi adapter for Codex

The harness adapter that lets [Pumi](https://github.com/getpumi/pumi) run OpenAI's Codex CLI. Pumi's runner starts it once per invocation, in the invocation's workspace. It reads a `pumi.harness.v1.Invocation` as one line of protojson from standard input and runs `codex exec --json --ignore-user-config` with the task on codex's standard input, the output's JSON Schema in `--output-schema` and configuration that translates the capability list. It writes lines of `pumi.harness.v1.Line` to standard output: the usage of the turn from codex's `turn.completed`, then the `pumi.harness.v1.Result`, with the refusals of the adapter's hook and the status `STATUS_LIMITED` when a provider's quota or rate limit stopped the run. Its log starts with `codex --version`. Closing standard input stops it.

The adapter offers both of Pumi's credential modes ([0240](https://github.com/getpumi/design/blob/main/decisions/0240-two-credential-modes.md)).

- **API key mode.** The invocation names Pumi's model gateway, which holds the API key. The adapter adds a provider of its own to codex's `model_providers`, at the gateway's address with the Responses API, that sends the invocation token in the `X-Pumi-Token` header from a variable (`env_http_headers`), so the token is not in codex's arguments. Codex asks for the invocation's model role by name, gets a home of its own with no sign-in, sends no call again by itself and does not try a WebSocket. The adapter clears the person's own OpenAI and Codex credentials and provider settings. The adapter never holds the key.
- **ChatGPT sign-in mode.** The invocation names no gateway. Codex runs on the person's own sign-in in their own `CODEX_HOME`, talks to OpenAI directly and asks for the role's model, and Pumi's runner counts and caps the usage that the adapter passes from `turn.completed` ([0241](https://github.com/getpumi/design/blob/main/decisions/0241-usage-on-the-sign-in-path.md)). When the person's environment sets `OPENAI_BASE_URL`, which codex itself ignores, the adapter passes it as `openai_base_url`.

## How the capability list is enforced

The adapter translates the capability list ([0159](https://github.com/getpumi/design/blob/main/decisions/0159-capability-list-and-core-confinement.md)) in two layers.

- **Codex's sandbox**, through a permission profile: codex and its commands may read the system's minimal files and the workspace, may write the write paths when the list has the edit kind or the kind that runs commands, and may write nothing else, including the temporary directory. Commands get no network. Codex offers its shell tool only when the list has the kind that runs commands or the edit kind.
- **A PreToolUse hook**: codex runs the adapter itself as the hook of every tool call. The hook allows a command only when the list has the kind that runs commands and the command is a simple command that one of the list's command entries begins, with the same `VAR=value` assignments ([0248](https://github.com/getpumi/design/blob/main/decisions/0248-commands-with-assignments.md)). It judges each path of a patch, from the apply_patch tool or an `apply_patch` command, against the write paths. It allows `view_image` only with the read kind, refuses subagents and MCP tools, and records each refusal for the result. The adapter turns off codex's command rules (`--ignore-rules`), because a rule that allows a command makes codex run it outside the sandbox, and its hosted web search, which no hook sees.

What Codex cannot express is left to the runner's confinement check, which compares the workspace with the write paths after the run:

- **Network hosts.** Codex has no tool that fetches a URL, and its sandbox gives commands the whole network or none. The adapter gives commands none, even when the list names hosts, so a list that allows hosts gets no network. The conformance suite's network scenario is skipped for this reason.
- **Reading.** Codex reads the workspace itself, such as its AGENTS.md, under the same profile, and stops when it cannot, so the profile cannot leave reading of the workspace out. Without the read kind, the hook refuses `view_image`, but a command that the list allows can still read the workspace.
- **Write paths with patterns.** Codex's profile takes only an exact path or a path ending in `/**`. A write path with another pattern is widened in the sandbox to its literal prefix followed by `/**`, or to the whole workspace when its first segment is a pattern. The hook judges the paths of a patch exactly, but a command that the list allows can write anywhere in the widened area.
- **A hook that fails.** Codex lets a tool call go on when its hook fails or times out. The adapter's hook writes a refusal whenever it cannot read its policy or the call, so only a hook that crashes or runs longer than 60 seconds lets a call through. The sandbox still limits what such a call writes.
- **Refusals of the sandbox.** Codex does not report a write or a network access that its sandbox refused; it shows only in the command's output. The adapter reports the refusals that its hook makes, and not those.

## Models that codex has no metadata for

Through the gateway, codex asks for the model role by name, such as `frontier`, a name it has no metadata for. Codex 0.172.0 then reports an `error` item in its events, "Model metadata for `frontier` not found. Defaulting to fallback metadata; this can degrade performance and cause issues.", and runs on, with a different set of tools: `exec_command` and `write_stdin` instead of its code-mode `exec`, and no apply_patch tool, so the model edits files with `apply_patch` commands, which the hook judges as edits. For a model it knows, such as `gpt-6.2-sol` on the sign-in path, codex offers the apply_patch tool, which the hook judges in the same way.

`make test` runs Pumi's adapter conformance suite, `getpumi.dev/pumi/conformance`, against the adapter and the real `codex` that the devcontainer pins, which calls the suite's fake Responses provider, in both credential modes.

Pumi is in early development and has no release yet.

- Design: [getpumi/design](https://github.com/getpumi/design) holds the settled design and the decision records.
- Work: the epics and ready work are in the [Pumi GitHub project](https://github.com/orgs/getpumi/projects/5).
- Contributing: see the organization's [CONTRIBUTING](https://github.com/getpumi/.github/blob/main/CONTRIBUTING.md). Every commit carries a `Signed-off-by` line under the Developer Certificate of Origin.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
