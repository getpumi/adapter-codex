// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: The Pumi Authors

package adapter

import (
	"path"
	"path/filepath"
	"strings"
)

// patchPaths returns the paths of the files that a patch of codex's
// apply_patch adds, updates, deletes or moves to, in order.
func patchPaths(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if p, ok := strings.CutPrefix(line, prefix); ok {
				out = append(out, strings.TrimSpace(p))
			}
		}
	}
	return out
}

// writable reports whether the capability list's write paths, globs
// relative to the workspace ws, allow a write of p: a path relative to the
// workspace or absolute. The globs are anchored to the workspace (regime
// PEX-007): "src/**" allows src/a.txt and not lib/src/a.txt, and a path
// outside the workspace is never writable.
func writable(globs []string, ws, p string) bool {
	if !filepath.IsAbs(p) {
		p = filepath.Join(ws, p)
	}
	rel, err := filepath.Rel(ws, filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, g := range globs {
		if matchGlob(strings.Split(path.Clean(g), "/"), strings.Split(rel, "/")) {
			return true
		}
	}
	return false
}

// matchGlob matches the segments of a path against the segments of a glob,
// where "**" matches any number of segments and every other segment is a
// pattern of path.Match.
func matchGlob(glob, segs []string) bool {
	if len(glob) == 0 {
		return len(segs) == 0
	}
	if glob[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchGlob(glob[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(glob[0], segs[0])
	return err == nil && ok && matchGlob(glob[1:], segs[1:])
}

// sandboxWritePath returns the path, relative to the workspace, that
// codex's sandbox makes writable for a write path. Codex 0.172.0 takes an
// exact path or a path that ends with "/**" for write access, and refuses
// other globs ("filesystem glob path `docs/*.txt` only supports `deny`
// access"), so a glob with a pattern before its end becomes the directory
// of its literal segments, with all below it. It also takes a writable path
// as a directory: with a path that names an existing file it fails before
// the command runs ("bwrap: Can't mkdir parents for src/a.txt/.git: Not a
// directory"), and it does not let a command create a file that the path
// names, so a write path with no pattern, which names one file, becomes its
// directory, with all below it. The hook still allows only what the glob
// matches for codex's edits, and the runner's confinement check covers
// what the commands write (decision 0159). The workspace itself is ".".
func sandboxWritePath(glob string) string {
	segs := strings.Split(path.Clean(glob), "/")
	for i, s := range segs {
		if s == "**" && i == len(segs)-1 {
			break
		}
		if !strings.ContainsAny(s, `*?[\`) {
			continue
		}
		if i == 0 {
			return "."
		}
		return strings.Join(segs[:i], "/") + "/**"
	}
	if segs[len(segs)-1] == "**" {
		if len(segs) == 1 {
			return "."
		}
		return strings.Join(segs, "/")
	}
	if len(segs) == 1 {
		return "."
	}
	return strings.Join(segs[:len(segs)-1], "/") + "/**"
}
