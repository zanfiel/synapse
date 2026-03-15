package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ProjectMap is a structural index of the entire codebase.
// Built on startup, it gives the agent a bird's-eye view of every file,
// function, type, and import — so it can navigate intelligently without
// reading every file first.
type ProjectMap struct {
	Root     string
	Language string
	Files    []FileEntry
	Stats    ProjectStats
}

type FileEntry struct {
	Path      string
	RelPath   string
	Lines     int
	Size      int64
	Language  string
	Symbols   []Symbol
}

type Symbol struct {
	Name string
	Kind string // "func", "type", "struct", "interface", "const", "var", "class", "method"
	Line int
}

type ProjectStats struct {
	TotalFiles int
	TotalLines int
	TotalSize  int64
	Languages  map[string]int // language -> file count
}

var languageExts = map[string]string{
	".go":    "go",
	".ts":    "typescript",
	".tsx":   "typescript",
	".js":    "javascript",
	".jsx":   "javascript",
	".py":    "python",
	".rs":    "rust",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".hpp":   "cpp",
	".java":  "java",
	".rb":    "ruby",
	".lua":   "lua",
	".zig":   "zig",
	".svelte": "svelte",
	".vue":   "vue",
	".sh":    "bash",
	".sql":   "sql",
	".md":    "markdown",
}

// Symbol extraction patterns per language
var symbolPatterns = map[string][]*regexp.Regexp{
	"go": {
		regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?(\w+)`),
		regexp.MustCompile(`^type\s+(\w+)\s+(struct|interface)`),
		regexp.MustCompile(`^var\s+(\w+)\s+`),
		regexp.MustCompile(`^const\s+(\w+)\s+`),
	},
	"typescript": {
		regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+(\w+)`),
		regexp.MustCompile(`^(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`),
		regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`),
		regexp.MustCompile(`^(?:export\s+)?type\s+(\w+)\s*=`),
		regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*[=:]`),
	},
	"javascript": {
		regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+(\w+)`),
		regexp.MustCompile(`^(?:export\s+)?class\s+(\w+)`),
		regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=`),
	},
	"python": {
		regexp.MustCompile(`^def\s+(\w+)\s*\(`),
		regexp.MustCompile(`^class\s+(\w+)`),
		regexp.MustCompile(`^(\w+)\s*=\s*`),
	},
	"rust": {
		regexp.MustCompile(`^(?:pub\s+)?fn\s+(\w+)`),
		regexp.MustCompile(`^(?:pub\s+)?struct\s+(\w+)`),
		regexp.MustCompile(`^(?:pub\s+)?enum\s+(\w+)`),
		regexp.MustCompile(`^(?:pub\s+)?trait\s+(\w+)`),
		regexp.MustCompile(`^(?:pub\s+)?type\s+(\w+)`),
		regexp.MustCompile(`^impl(?:\s*<[^>]*>)?\s+(\w+)`),
	},
	"c": {
		regexp.MustCompile(`^(?:\w+[\s*]+)+(\w+)\s*\(`),
		regexp.MustCompile(`^(?:typedef\s+)?struct\s+(\w+)`),
		regexp.MustCompile(`^(?:typedef\s+)?enum\s+(\w+)`),
		regexp.MustCompile(`^#define\s+(\w+)`),
	},
}

var defaultIgnoreDirs = map[string]bool{
	"node_modules": true, ".git": true, "vendor": true, "dist": true,
	"build": true, "target": true, "__pycache__": true, ".next": true,
	".nuxt": true, ".venv": true, "venv": true, ".terraform": true,
	".gradle": true, ".idea": true, ".vscode": true, "coverage": true,
	"AppData": true, "Android": true, "Library": true, ".cache": true,
	".local": true, ".npm": true, ".cargo": true, ".rustup": true,
	"Downloads": true, "Documents": true, "Pictures": true, "Music": true,
	"Videos": true, "Desktop": true, ".docker": true, ".kube": true,
}

func isProjectRoot(dir string) bool {
	for _, marker := range projectRootMarkers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// projectRootMarkers are files that indicate "this is a project directory"
var projectRootMarkers = []string{
	"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "setup.py",
	"pom.xml", "build.gradle", "CMakeLists.txt", "Makefile", ".git",
	"deno.json", "bun.lockb", "composer.json", "Gemfile", "mix.exs",
	"build.zig", "meson.build", "flake.nix", "justfile",
}

// BuildProjectMap scans the workspace and extracts structural information.
// Only runs in actual project directories (must have a root marker).
// Caps at 200 files and skips giant directories.
func BuildProjectMap(workDir string) *ProjectMap {
	// Don't index home directories or non-project dirs
	if !isProjectRoot(workDir) {
		return nil
	}

	// Skip home directories — they contain huge SDK/cache trees that bloat
	// the system prompt and slow startup (e.g. Android SDK, node_modules, etc.)
	if home, err := os.UserHomeDir(); err == nil {
		clean := filepath.Clean(workDir)
		if clean == filepath.Clean(home) {
			return nil
		}
	}

	pm := &ProjectMap{
		Root: workDir,
		Stats: ProjectStats{
			Languages: make(map[string]int),
		},
	}

	const maxFiles = 200

	filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if pm.Stats.TotalFiles >= maxFiles {
			return filepath.SkipAll
		}

		// Skip ignored directories
		if info.IsDir() {
			name := info.Name()
			if defaultIgnoreDirs[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		lang, ok := languageExts[ext]
		if !ok {
			return nil
		}

		rel, _ := filepath.Rel(workDir, path)

		entry := FileEntry{
			Path:     path,
			RelPath:  rel,
			Size:     info.Size(),
			Language: lang,
		}

		// Read file for line count and symbol extraction
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		entry.Lines = len(lines)

		// Extract symbols
		patterns := symbolPatterns[lang]
		if patterns == nil {
			// Use typescript patterns for svelte/vue
			if lang == "svelte" || lang == "vue" {
				patterns = symbolPatterns["typescript"]
			}
		}

		if patterns != nil {
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				for _, pat := range patterns {
					if m := pat.FindStringSubmatch(trimmed); m != nil {
						kind := detectSymbolKind(trimmed, lang)
						entry.Symbols = append(entry.Symbols, Symbol{
							Name: m[1],
							Kind: kind,
							Line: i + 1,
						})
						break
					}
				}
			}
		}

		pm.Files = append(pm.Files, entry)
		pm.Stats.TotalFiles++
		pm.Stats.TotalLines += entry.Lines
		pm.Stats.TotalSize += info.Size()
		pm.Stats.Languages[lang]++

		return nil
	})

	// Detect primary language
	maxCount := 0
	for lang, count := range pm.Stats.Languages {
		if count > maxCount {
			maxCount = count
			pm.Language = lang
		}
	}

	// Sort files by path
	sort.Slice(pm.Files, func(i, j int) bool {
		return pm.Files[i].RelPath < pm.Files[j].RelPath
	})

	return pm
}

func detectSymbolKind(line, lang string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.HasPrefix(lower, "func") || strings.HasPrefix(lower, "def ") ||
		strings.Contains(lower, "function ") || strings.HasPrefix(lower, "pub fn") ||
		strings.HasPrefix(lower, "fn "):
		if strings.Contains(line, ") ") && strings.Contains(line, "(") {
			// Method (has receiver)
			return "method"
		}
		return "func"
	case strings.Contains(lower, "struct "):
		return "struct"
	case strings.Contains(lower, "interface "):
		return "interface"
	case strings.Contains(lower, "class "):
		return "class"
	case strings.Contains(lower, "enum "):
		return "enum"
	case strings.Contains(lower, "trait "):
		return "trait"
	case strings.HasPrefix(lower, "type "):
		return "type"
	case strings.HasPrefix(lower, "const "):
		return "const"
	case strings.HasPrefix(lower, "var ") || strings.HasPrefix(lower, "let "):
		return "var"
	case strings.HasPrefix(lower, "#define"):
		return "macro"
	default:
		return "symbol"
	}
}

// Summary returns a compact project overview for the system prompt.
// Hard-capped at ~4KB to avoid bloating the context.
func (pm *ProjectMap) Summary() string {
	if len(pm.Files) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## Project Map (%d files, %d lines, %s)\n",
		pm.Stats.TotalFiles, pm.Stats.TotalLines, humanBytes(pm.Stats.TotalSize)))

	// Language breakdown
	sb.WriteString("Languages: ")
	var langs []string
	for lang, count := range pm.Stats.Languages {
		langs = append(langs, fmt.Sprintf("%s(%d)", lang, count))
	}
	sort.Strings(langs)
	sb.WriteString(strings.Join(langs, ", ") + "\n\n")

	// File tree with symbols — cap total output
	const maxBytes = 4096
	for _, f := range pm.Files {
		symbolStr := ""
		if len(f.Symbols) > 0 {
			var names []string
			for _, s := range f.Symbols {
				names = append(names, s.Name)
			}
			// Limit to 10 symbols per file in summary
			if len(names) > 10 {
				names = append(names[:10], fmt.Sprintf("...+%d", len(names)-10))
			}
			symbolStr = " → " + strings.Join(names, ", ")
		}
		line := fmt.Sprintf("  %s (%d lines)%s\n", f.RelPath, f.Lines, symbolStr)
		if sb.Len()+len(line) > maxBytes {
			sb.WriteString(fmt.Sprintf("  ... +%d more files\n", len(pm.Files)-pm.Stats.TotalFiles))
			break
		}
		sb.WriteString(line)
	}

	return sb.String()
}

// FindSymbol searches for a symbol by name across all files.
func (pm *ProjectMap) FindSymbol(name string) []FileEntry {
	var results []FileEntry
	lower := strings.ToLower(name)

	for _, f := range pm.Files {
		for _, s := range f.Symbols {
			if strings.ToLower(s.Name) == lower || strings.Contains(strings.ToLower(s.Name), lower) {
				results = append(results, f)
				break
			}
		}
	}
	return results
}

func humanBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
