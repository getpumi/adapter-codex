// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"regexp"
	"slices"
	"strings"
)

// commandAllowed reports whether a command that the model asks codex to
// run is allowed by one of the capability list's command entries (decisions
// 0159 and 0248). Codex runs the command with a shell, so the command must
// be one simple command: words, quoted or not, with no operator, redirect,
// substitution or expansion that would make the shell run or read anything
// beyond it. An entry allows a command whose words start with the entry's
// words and whose leading NAME=value assignments are exactly the entry's.
func commandAllowed(entries []string, command string) bool {
	assigned, words, ok := simpleCommand(command)
	if !ok || len(words) == 0 {
		return false
	}
	for _, e := range entries {
		ea, ew, ok := simpleCommand(e)
		if !ok || len(ew) == 0 || len(ew) > len(words) {
			continue
		}
		if sameAssignments(ea, assigned) && slices.Equal(ew, words[:len(ew)]) {
			return true
		}
	}
	return false
}

// assignment matches a word that assigns a variable.
var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// simpleCommand splits a command into its leading assignments and its
// words, as a POSIX shell would, and reports whether it is one simple
// command that the adapter can judge.
func simpleCommand(command string) (assigned, words []string, ok bool) {
	all, ok := shellWords(command)
	if !ok {
		return nil, nil, false
	}
	i := 0
	for i < len(all) && assignment.MatchString(all[i].text) && !all[i].quotedName {
		i++
	}
	for _, w := range all[:i] {
		assigned = append(assigned, w.text)
	}
	for _, w := range all[i:] {
		words = append(words, w.text)
	}
	return assigned, words, true
}

func sameAssignments(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// word is one word of a command. quotedName says that a quote starts
// before its first "=", so that the shell does not read it as an
// assignment.
type word struct {
	text       string
	quotedName bool
}

// unquotedSpecial are the characters that, outside quotes, make the shell
// do more than run one command with literal words: operators, redirects,
// substitutions, expansions, subshells and the end of a line.
const unquotedSpecial = ";&|<>()$`\n\r"

// shellWords splits a command into words with the quoting rules of a POSIX
// shell: single quotes, double quotes and backslashes. It reports false
// for a character in unquotedSpecial outside quotes, a "$" or "`" inside
// double quotes, and a quote that is not closed.
func shellWords(s string) ([]word, bool) {
	var words []word
	var cur strings.Builder
	in, quotedName, eq := false, false, false
	flush := func() {
		if in {
			words = append(words, word{text: cur.String(), quotedName: quotedName})
		}
		cur.Reset()
		in, quotedName, eq = false, false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
		case strings.IndexByte(unquotedSpecial, c) >= 0:
			return nil, false
		case c == '\\' || c == '\'' || c == '"':
			end, ok := quotedPart(s, i, &cur)
			if !ok {
				return nil, false
			}
			in = true
			quotedName = quotedName || !eq
			i = end
		default:
			in = true
			eq = eq || c == '='
			cur.WriteByte(c)
		}
	}
	flush()
	return words, true
}

// quotedPart writes the literal text of the backslash escape or the quoted
// string that starts at s[i] to b, and returns the index of its last byte.
func quotedPart(s string, i int, b *strings.Builder) (int, bool) {
	switch s[i] {
	case '\\':
		if i+1 >= len(s) || s[i+1] == '\n' {
			return 0, false
		}
		b.WriteByte(s[i+1])
		return i + 1, true
	case '\'':
		j := strings.IndexByte(s[i+1:], '\'')
		if j < 0 {
			return 0, false
		}
		b.WriteString(s[i+1 : i+1+j])
		return i + j + 1, true
	default:
		n, ok := doubleQuoted(s[i+1:], b)
		if !ok {
			return 0, false
		}
		return i + n + 1, true
	}
}

// doubleQuoted writes the content of a double-quoted string that starts at
// s, after its opening quote, to b, and returns the length up to the
// closing quote. It reports false for a "$" or "`", which the shell would
// expand, and for a string that is not closed.
func doubleQuoted(s string, b *strings.Builder) (int, bool) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return i, true
		case '$', '`':
			return 0, false
		case '\\':
			if i+1 < len(s) && strings.IndexByte("\"\\", s[i+1]) >= 0 {
				i++
				b.WriteByte(s[i])
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return 0, false
}

// heredoc matches the first line of an apply_patch command with a here
// document, and its delimiter.
var heredoc = regexp.MustCompile(`^apply_patch <<-?(?:'(\w+)'|"(\w+)"|(\w+))$`)

// patchCommand returns the patch of a command that only runs apply_patch,
// which codex carries out as an edit of its own rather than as a command:
// apply_patch with the patch in a here document, or as one single-quoted
// argument.
func patchCommand(command string) (string, bool) {
	first, rest, _ := strings.Cut(command, "\n")
	if m := heredoc.FindStringSubmatch(strings.TrimSpace(first)); m != nil {
		delim := m[1] + m[2] + m[3]
		body, ok := strings.CutSuffix(strings.TrimRight(rest, "\n"), delim)
		if !ok || (body != "" && !strings.HasSuffix(body, "\n")) {
			return "", false
		}
		return body, true
	}
	if p, ok := strings.CutPrefix(strings.TrimSpace(command), "apply_patch '"); ok {
		if p, ok := strings.CutSuffix(p, "'"); ok && !strings.Contains(p, "'") {
			return p, true
		}
	}
	return "", false
}
