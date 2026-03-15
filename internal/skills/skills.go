package skills

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed templates/*.md
var builtins embed.FS

// Skill represents a named task template.
type Skill struct {
	Name        string
	Description string
	Template    string
}

var registry = map[string]*Skill{}

func init() {
	entries, _ := builtins.ReadDir("templates")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := builtins.ReadFile("templates/" + e.Name())
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		registry[name] = parseSkill(name, string(data))
	}
}

// LoadUserSkills loads skills from configDir/skills/*.md, overriding built-ins.
func LoadUserSkills(configDir string) {
	skillsDir := filepath.Join(configDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(skillsDir, e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		registry[name] = parseSkill(name, string(data))
	}
}

func parseSkill(name, content string) *Skill {
	desc := ""
	lines := strings.SplitN(content, "\n", 3)
	if len(lines) > 0 {
		first := lines[0]
		// Strip comment markers: <!-- ... --> or // ...
		first = strings.TrimPrefix(first, "<!--")
		first = strings.TrimSuffix(first, "-->")
		first = strings.TrimPrefix(first, "//")
		desc = strings.TrimSpace(first)
	}
	return &Skill{Name: name, Description: desc, Template: strings.TrimSpace(content)}
}

// Get returns a skill by name.
func Get(name string) (*Skill, bool) {
	s, ok := registry[name]
	return s, ok
}

// All returns all registered skills sorted by name.
func All() []*Skill {
	var out []*Skill
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns all registered skill names.
func Names() []string {
	var names []string
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
