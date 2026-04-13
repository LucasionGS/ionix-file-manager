// Package git provides lightweight Git status integration for file entries.
// It shells out to git to discover the repo root and file statuses, caching
// the repo root to avoid redundant traversals when navigating deeper into an
// already-discovered repository.
package git

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// FileStatus represents the Git status of a single file or directory.
type FileStatus int

const (
	StatusNone      FileStatus = iota // not in a git repo or unmodified / clean
	StatusModified                    // modified in worktree
	StatusStaged                      // staged (index) changes
	StatusUntracked                   // untracked file
	StatusAdded                       // new file added to index
	StatusDeleted                     // deleted
	StatusRenamed                     // renamed
	StatusConflict                    // merge conflict
	StatusIgnored                     // ignored by .gitignore
)

// Label returns a short display string for the status.
func (s FileStatus) Label() string {
	switch s {
	case StatusModified:
		return "M"
	case StatusStaged:
		return "S"
	case StatusUntracked:
		return "?"
	case StatusAdded:
		return "A"
	case StatusDeleted:
		return "D"
	case StatusRenamed:
		return "R"
	case StatusConflict:
		return "C"
	case StatusIgnored:
		return "I"
	default:
		return ""
	}
}

// RepoState holds cached information about a Git repository.
type RepoState struct {
	Root     string                // absolute path to repo root (empty = not in a repo)
	Statuses map[string]FileStatus // path relative to CWD → status
}

// DetectRepo finds the Git repository root for dir.
//
// If cachedRoot is non-empty and dir is a subdirectory of cachedRoot, the
// function skips the expensive rev-parse call and returns cachedRoot directly.
// This avoids duplicate work when navigating deeper into a known repo.
func DetectRepo(dir, cachedRoot string) string {
	// Optimisation: if we already know the repo root and we're still inside
	// it, reuse it. We only re-detect when navigating *outside* the cached root.
	if cachedRoot != "" && isSubdir(dir, cachedRoot) {
		return cachedRoot
	}

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Branch returns the current branch name for the given repo root.
// Returns empty string if not on a branch (detached HEAD, etc.).
func Branch(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Status runs `git status` in dir and returns a map of entry names to their
// status. The keys are base names (not full paths) so they can be matched
// directly against fs.Entry.Name values.
//
// Only entries whose paths fall directly inside dir are included.
func Status(dir, repoRoot string) map[string]FileStatus {
	if repoRoot == "" {
		return nil
	}

	cmd := exec.Command("git", "-C", repoRoot, "status", "--porcelain=v1", "--ignored", "-u")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	result := make(map[string]FileStatus)
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		x, y := line[0], line[1]
		relPath := line[3:]

		// Handle renames: "R  old -> new"
		if idx := strings.Index(relPath, " -> "); idx >= 0 {
			relPath = relPath[idx+4:]
		}

		absPath := filepath.Join(repoRoot, relPath)
		entryDir := filepath.Dir(absPath)
		entryName := filepath.Base(absPath)

		// For directories that contain changes, propagate the status up.
		// Check if this file is directly in `dir` or in a subdirectory of `dir`.
		if entryDir == dir {
			// Direct child of the current directory.
			merge(&result, entryName, parseStatus(x, y))
		} else if isSubdir(absPath, dir) {
			// File is in a subdirectory of dir — attribute it to the immediate child dir.
			rel, err := filepath.Rel(dir, absPath)
			if err != nil {
				continue
			}
			topDir := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			merge(&result, topDir, parseStatus(x, y))
		}
	}

	return result
}

// isSubdir reports whether child is inside parent (or equal to it).
func isSubdir(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// merge sets the status for name, preferring "more important" statuses
// (e.g. conflict > modified > untracked).
func merge(m *map[string]FileStatus, name string, s FileStatus) {
	if s == StatusNone || s == StatusIgnored {
		if _, ok := (*m)[name]; !ok {
			(*m)[name] = s
		}
		return
	}
	existing, ok := (*m)[name]
	if !ok || priority(s) > priority(existing) {
		(*m)[name] = s
	}
}

func priority(s FileStatus) int {
	switch s {
	case StatusConflict:
		return 7
	case StatusDeleted:
		return 6
	case StatusModified:
		return 5
	case StatusRenamed:
		return 4
	case StatusAdded:
		return 3
	case StatusStaged:
		return 2
	case StatusUntracked:
		return 1
	case StatusIgnored:
		return -1
	default:
		return 0
	}
}

func parseStatus(x, y byte) FileStatus {
	// Unmerged (conflict) codes.
	if x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D') {
		return StatusConflict
	}
	if x == '!' && y == '!' {
		return StatusIgnored
	}
	if x == '?' && y == '?' {
		return StatusUntracked
	}
	// Index (staged) takes precedence visually.
	switch x {
	case 'A':
		return StatusAdded
	case 'D':
		return StatusDeleted
	case 'R':
		return StatusRenamed
	case 'M':
		return StatusStaged
	}
	// Worktree changes.
	switch y {
	case 'M':
		return StatusModified
	case 'D':
		return StatusDeleted
	}
	return StatusNone
}
