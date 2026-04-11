package fs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyEntry copies a file or directory (recursively) from src to dst.
func CopyEntry(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else {
			fi, _ := e.Info()
			var mode os.FileMode = 0644
			if fi != nil {
				mode = fi.Mode()
			}
			if err := copyFile(s, d, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// MkdirEntry creates a new directory at path.
func MkdirEntry(path string) error {
	return os.MkdirAll(path, 0755)
}

// CreateFileEntry creates a new empty file at path (like touch — no-op if already exists).
func CreateFileEntry(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// ExpandBraces expands a bash-style brace expression into all matching strings.
// e.g. "a/{b,c}/{1,2}" → ["a/b/1", "a/b/2", "a/c/1", "a/c/2"]
// Returns an error for unmatched braces, empty alternatives, or path traversal.
func ExpandBraces(pattern string) ([]string, error) {
	// Reject path traversal.
	for _, seg := range strings.Split(pattern, "/") {
		clean := strings.Trim(seg, " ")
		if clean == ".." || clean == "." {
			return nil, fmt.Errorf("path traversal not allowed: %q", seg)
		}
	}
	// Reject absolute paths.
	if strings.HasPrefix(pattern, "/") {
		return nil, fmt.Errorf("pattern must be relative, not absolute")
	}
	// Validate brace matching.
	depth := 0
	for i, c := range pattern {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unexpected '}' at position %d", i)
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unclosed '{' in pattern")
	}
	return expandBraces(pattern)
}

// expandBraces is the internal recursive expander (no validation).
func expandBraces(pattern string) ([]string, error) {
	start := -1
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '{' {
			start = i
			break
		}
	}
	if start == -1 {
		return []string{pattern}, nil
	}
	depth, end := 0, -1
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	prefix := pattern[:start]
	suffix := pattern[end+1:]
	alts := splitBraceAlts(pattern[start+1 : end])
	for _, alt := range alts {
		if strings.TrimSpace(alt) == "" {
			return nil, fmt.Errorf("empty alternative in braces %q", pattern[start:end+1])
		}
	}
	var result []string
	for _, alt := range alts {
		expanded, err := expandBraces(prefix + alt + suffix)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}
	return result, nil
}

// splitBraceAlts splits s by top-level commas (not inside nested braces).
func splitBraceAlts(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

// DeleteEntry removes a file or directory (recursively).
func DeleteEntry(path string) error {
	return os.RemoveAll(path)
}

// MoveEntry moves src to dst. Falls back to copy+delete on cross-device moves.
func MoveEntry(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := CopyEntry(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}
