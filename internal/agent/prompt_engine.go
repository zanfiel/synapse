package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PromptEngine builds context-aware system prompts.
// Inspired by how Cursor, Claude Code, and Windsurf structure their agent instructions.
type PromptEngine struct {
	workDir     string
	projectName string
	projectLang string
	framework   string
	gitBranch   string
	gitDirty    int
	engramCtx   string // pre-fetched Engram context
	customRules string // from .synapse or AGENTS.md
}

// DetectProject scans the working directory to determine project type.
func DetectProject(workDir string) *PromptEngine {
	pe := &PromptEngine{
		workDir:     workDir,
		projectName: filepath.Base(workDir),
	}

	// Language detection by config files
	markers := map[string]struct{ lang, framework string }{
		"go.mod":          {"Go", ""},
		"Cargo.toml":      {"Rust", ""},
		"package.json":    {"JavaScript/TypeScript", ""},
		"pyproject.toml":  {"Python", ""},
		"requirements.txt": {"Python", ""},
		"Gemfile":          {"Ruby", ""},
		"pom.xml":          {"Java", "Maven"},
		"build.gradle":     {"Java", "Gradle"},
		"pubspec.yaml":     {"Dart", "Flutter"},
		"composer.json":    {"PHP", ""},
		"mix.exs":          {"Elixir", ""},
		"deno.json":        {"TypeScript", "Deno"},
		"bun.lockb":        {"TypeScript", "Bun"},
		"bun.lock":         {"TypeScript", "Bun"},
	}

	for file, info := range markers {
		if _, err := os.Stat(filepath.Join(workDir, file)); err == nil {
			pe.projectLang = info.lang
			if info.framework != "" {
				pe.framework = info.framework
			}
			break
		}
	}

	// Framework detection (more specific)
	frameworkMarkers := map[string]string{
		"svelte.config.js":   "SvelteKit",
		"svelte.config.ts":   "SvelteKit",
		"next.config.js":     "Next.js",
		"next.config.mjs":    "Next.js",
		"nuxt.config.ts":     "Nuxt",
		"astro.config.mjs":   "Astro",
		"vite.config.ts":     "Vite",
		"tsconfig.json":      "TypeScript",
		"tailwind.config.js": "Tailwind CSS",
		"tailwind.config.ts": "Tailwind CSS",
		"Dockerfile":         "Docker",
		"docker-compose.yml": "Docker Compose",
		"flake.nix":          "Nix",
		".github/workflows":  "GitHub Actions",
	}

	for file, fw := range frameworkMarkers {
		if _, err := os.Stat(filepath.Join(workDir, file)); err == nil {
			if pe.framework == "" {
				pe.framework = fw
			} else {
				pe.framework += " + " + fw
			}
		}
	}

	// Load custom rules from .synapse file or AGENTS.md
	for _, ruleFile := range []string{".synapse", "AGENTS.md", ".cursorrules", ".claude"} {
		path := filepath.Join(workDir, ruleFile)
		if data, err := os.ReadFile(path); err == nil {
			pe.customRules = string(data)
			if len(pe.customRules) > 4000 {
				pe.customRules = pe.customRules[:4000] + "\n... (truncated)"
			}
			break
		}
	}

	return pe
}

// SetEngramContext injects pre-fetched Engram context.
func (pe *PromptEngine) SetEngramContext(ctx string) {
	pe.engramCtx = ctx
}

// SetGitStatus sets current git branch and dirty file count.
func (pe *PromptEngine) SetGitStatus(branch string, dirty int) {
	pe.gitBranch = branch
	pe.gitDirty = dirty
}

// Build generates the complete system prompt.
func (pe *PromptEngine) Build(model, reasoning string, toolNames []string) string {
	var sb strings.Builder

	// Core identity
	sb.WriteString("You are Synapse, a precise and autonomous coding agent. ")
	sb.WriteString("You execute tasks directly using your tools — don't explain what you'd do, just do it.\n\n")

	// Environment context
	sb.WriteString("## Environment\n")
	sb.WriteString(fmt.Sprintf("- Date: %s\n", time.Now().Format("Monday, January 2, 2006 3:04 PM MST")))
	sb.WriteString(fmt.Sprintf("- Working directory: %s\n", pe.workDir))
	sb.WriteString(fmt.Sprintf("- Model: %s", model))
	if reasoning != "" && reasoning != "off" {
		sb.WriteString(fmt.Sprintf(" (reasoning: %s)", reasoning))
	}
	sb.WriteString("\n")

	if pe.projectLang != "" {
		sb.WriteString(fmt.Sprintf("- Language: %s\n", pe.projectLang))
	}
	if pe.framework != "" {
		sb.WriteString(fmt.Sprintf("- Framework: %s\n", pe.framework))
	}
	if pe.gitBranch != "" {
		sb.WriteString(fmt.Sprintf("- Git: %s", pe.gitBranch))
		if pe.gitDirty > 0 {
			sb.WriteString(fmt.Sprintf(" (%d uncommitted)", pe.gitDirty))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Available tools
	sb.WriteString("## Tools\n")
	sb.WriteString(fmt.Sprintf("You have %d tools: %s\n\n", len(toolNames), strings.Join(toolNames, ", ")))

	// Tool-use rules (borrowed from Cursor/Claude Code patterns)
	sb.WriteString("## Rules\n")
	sb.WriteString("1. **Read before edit** — always read a file before modifying it. Never guess at content.\n")
	sb.WriteString("2. **One step at a time** — make one change, verify it works, then proceed.\n")
	sb.WriteString("3. **Use grep/glob to find** — don't assume file locations. Search first.\n")
	sb.WriteString("4. **Minimal diffs** — use `edit` for surgical changes. Only use `write` for new files or complete rewrites.\n")
	sb.WriteString("5. **Verify after changes** — run tests, builds, or diagnostics after code changes.\n")
	sb.WriteString("6. **Ask if unclear** — if requirements are ambiguous, ask. Don't guess the user's intent.\n")
	sb.WriteString("7. **Memory persistence** — use memory_store for important work, decisions, discoveries. Use memory_list at session start.\n")
	sb.WriteString("8. **SSH safety** — never lock out SSH access. Test connectivity before modifying auth.\n")
	sb.WriteString("9. **Be concise** — show results, not process. Don't narrate every tool call.\n")

	// Language-specific rules
	if pe.projectLang != "" {
		sb.WriteString(pe.langRules())
	}
	sb.WriteString("\n")

	// Custom project rules
	if pe.customRules != "" {
		sb.WriteString("## Project Rules\n")
		sb.WriteString(pe.customRules)
		sb.WriteString("\n\n")
	}

	// Engram context (pre-fetched memories, permanent facts, recent activity)
	if pe.engramCtx != "" {
		sb.WriteString("<engram-context>\n")
		sb.WriteString(pe.engramCtx)
		sb.WriteString("\n</engram-context>\n\n")
	}

	return sb.String()
}

// langRules returns language-specific coding rules.
func (pe *PromptEngine) langRules() string {
	var sb strings.Builder

	switch {
	case strings.Contains(pe.projectLang, "Go"):
		sb.WriteString("\n### Go Rules\n")
		sb.WriteString("- Run `go build ./...` after changes to verify compilation.\n")
		sb.WriteString("- Use `go vet` and check for errors, don't ignore them.\n")
		sb.WriteString("- Follow standard Go project layout (cmd/, internal/, pkg/).\n")
		sb.WriteString("- Handle errors explicitly — never use `_` for error returns.\n")

	case strings.Contains(pe.projectLang, "TypeScript"), strings.Contains(pe.projectLang, "JavaScript"):
		sb.WriteString("\n### TypeScript Rules\n")
		sb.WriteString("- Check for type errors with tsc or diagnostics tool after changes.\n")
		sb.WriteString("- Prefer const over let. Never use var.\n")
		sb.WriteString("- Use async/await over .then() chains.\n")
		if strings.Contains(pe.framework, "Svelte") {
			sb.WriteString("- Use Svelte 5 runes ($state, $derived, $effect). No legacy reactive statements.\n")
		}
		if strings.Contains(pe.framework, "Bun") {
			sb.WriteString("- Use Bun APIs where available (Bun.file, Bun.serve, etc.).\n")
		}

	case strings.Contains(pe.projectLang, "Rust"):
		sb.WriteString("\n### Rust Rules\n")
		sb.WriteString("- Run `cargo check` after changes.\n")
		sb.WriteString("- Use Result/Option properly — no unwrap() in library code.\n")
		sb.WriteString("- Derive common traits (Debug, Clone) on public types.\n")

	case strings.Contains(pe.projectLang, "Python"):
		sb.WriteString("\n### Python Rules\n")
		sb.WriteString("- Use type hints on all function signatures.\n")
		sb.WriteString("- Use pathlib.Path over os.path.\n")
		sb.WriteString("- Use f-strings for formatting.\n")

	case strings.Contains(pe.projectLang, "Dart"):
		sb.WriteString("\n### Dart/Flutter Rules\n")
		sb.WriteString("- Use const constructors where possible.\n")
		sb.WriteString("- Follow Material 3 design guidelines.\n")
		sb.WriteString("- Extract widgets into separate files when > 100 lines.\n")
	}

	return sb.String()
}
