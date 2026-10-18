package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Prompt templates live in nas-agents/templates/<group>/<name>.md. The cloud agent
// fills {{placeholders}} instead of writing local prompts from scratch: cheaper in
// cloud tokens and more stable output from small local models.
//
// Optional front matter (single-line `key: value`): system, max_tokens, profile.

type Template struct {
	Name      string
	System    string
	Body      string
	MaxTokens int
	Profile   string // recommended profile
	Vars      []string
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func (c Config) loadTemplate(name string) (Template, error) {
	t := Template{Name: name}
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return t, fmt.Errorf("invalid template name %q", name)
	}
	data, err := os.ReadFile(filepath.Join(c.TemplatesDir, name+".md"))
	if err != nil {
		return t, fmt.Errorf("template %q not found (see local_capabilities for the list)", name)
	}
	text := string(data)
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---\n"); end >= 0 {
			for _, line := range strings.Split(text[4:4+end], "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				v = strings.TrimSpace(v)
				switch strings.TrimSpace(k) {
				case "system":
					t.System = v
				case "max_tokens":
					t.MaxTokens, _ = strconv.Atoi(v)
				case "profile":
					t.Profile = v
				}
			}
			text = text[4+end+5:]
		}
	}
	t.Body = strings.TrimSpace(text)
	seen := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(t.System+"\n"+t.Body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			t.Vars = append(t.Vars, m[1])
		}
	}
	return t, nil
}

// render substitutes vars; every placeholder must be provided.
func (t Template) render(vars map[string]string) (system, body string, err error) {
	var missing []string
	for _, v := range t.Vars {
		if _, ok := vars[v]; !ok {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("template %q needs vars: %s", t.Name, strings.Join(missing, ", "))
	}
	sub := func(s string) string {
		return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
			return vars[placeholderRe.FindStringSubmatch(m)[1]]
		})
	}
	return sub(t.System), sub(t.Body), nil
}

// listTemplates returns "group/name -> vars" for local_capabilities.
func (c Config) listTemplates() map[string][]string {
	out := map[string][]string{}
	matches, _ := filepath.Glob(filepath.Join(c.TemplatesDir, "*", "*.md"))
	sort.Strings(matches)
	for _, p := range matches {
		rel, _ := filepath.Rel(c.TemplatesDir, p)
		name := strings.TrimSuffix(rel, ".md")
		if t, err := c.loadTemplate(name); err == nil {
			out[name] = t.Vars
		}
	}
	return out
}
