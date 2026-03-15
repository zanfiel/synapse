package git

import (
	"os/exec"
	"path/filepath"
	"strings"
)

type Info struct {
	IsRepo     bool
	Branch     string
	Dirty      bool
	Ahead      int
	Behind     int
	RemoteURL  string
	Root       string
	Changes    []string // short status lines
}

// Detect returns git info for the given directory, or nil if not a repo.
func Detect(dir string) *Info {
	root, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}

	info := &Info{
		IsRepo: true,
		Root:   strings.TrimSpace(root),
	}

	if branch, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}

	if status, err := run(dir, "status", "--porcelain"); err == nil {
		status = strings.TrimSpace(status)
		if status != "" {
			info.Dirty = true
			info.Changes = strings.Split(status, "\n")
			if len(info.Changes) > 20 {
				info.Changes = info.Changes[:20]
			}
		}
	}

	if remote, err := run(dir, "config", "--get", "remote.origin.url"); err == nil {
		info.RemoteURL = strings.TrimSpace(remote)
	}

	// Ahead/behind
	if ab, err := run(dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		parts := strings.Fields(strings.TrimSpace(ab))
		if len(parts) == 2 {
			for _, c := range parts[0] {
				info.Ahead = info.Ahead*10 + int(c-'0')
			}
			for _, c := range parts[1] {
				info.Behind = info.Behind*10 + int(c-'0')
			}
		}
	}

	return info
}

func DiffStaged(dir string) (string, error) {
	return run(dir, "diff", "--cached", "--stat")
}

func DiffUnstaged(dir string) (string, error) {
	return run(dir, "diff", "--stat")
}

func Log(dir string, n int) (string, error) {
	return run(dir, "log", "--oneline", "-n", itoa(n))
}

func CommitAll(dir, message string) (string, error) {
	if _, err := run(dir, "add", "-A"); err != nil {
		return "", err
	}
	return run(dir, "commit", "-m", message)
}

func CurrentDiff(dir string) (string, error) {
	return run(dir, "diff", "HEAD")
}

func ShortStatus(dir string) (string, error) {
	return run(dir, "status", "-sb")
}

// Summary returns a one-line git context string.
func Summary(dir string) string {
	info := Detect(dir)
	if info == nil {
		return ""
	}

	s := "git:" + info.Branch
	if info.Dirty {
		s += "*"
	}
	if info.Ahead > 0 {
		s += "↑" + itoa(info.Ahead)
	}
	if info.Behind > 0 {
		s += "↓" + itoa(info.Behind)
	}
	changedFiles := len(info.Changes)
	if changedFiles > 0 {
		s += " (" + itoa(changedFiles) + " changed)"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// FindProjectRoot walks up from dir to find .git, returns the git root or dir itself.
func FindProjectRoot(dir string) string {
	info := Detect(dir)
	if info != nil {
		return info.Root
	}
	abs, _ := filepath.Abs(dir)
	return abs
}

// BranchAndDirty returns the current branch name and count of dirty files.
func BranchAndDirty(dir string) (string, int) {
	info := Detect(dir)
	if info == nil {
		return "", 0
	}
	return info.Branch, len(info.Changes)
}
