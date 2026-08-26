package dashboard

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	lucideAttributePattern = regexp.MustCompile(`data-lucide="([^"]+)"`)
	panelIconPattern       = regexp.MustCompile(`data-sandbox-info-panel-icon="([a-z][a-z0-9-]+)"`)
	templateIconPattern    = regexp.MustCompile(`}}([a-z][a-z0-9-]+)`)
	quotedIconPattern      = regexp.MustCompile(`['"]([a-z][a-z0-9-]+)['"]`)
)

func TestDashboardIconCatalogMatchesUsage(t *testing.T) {
	catalog := make(map[string]bool, len(dashboardIconCatalog))
	targets := make(map[string]bool, len(dashboardIconCatalog))
	for _, icon := range dashboardIconCatalog {
		if icon.Target == "" || icon.Icon == "" || icon.Category == "" || icon.Purpose == "" {
			t.Errorf("incomplete icon definition: %#v", icon)
		}
		if targets[icon.Target] {
			t.Errorf("duplicate icon target %q", icon.Target)
		}
		targets[icon.Target] = true
		catalog[icon.Icon] = true
	}

	used := make(map[string]bool)
	err := fs.WalkDir(webFiles, "web", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (!strings.HasSuffix(path, ".html") && path != "web/assets/app.js") {
			return nil
		}
		body, err := fs.ReadFile(webFiles, path)
		if err != nil {
			return err
		}
		text := string(body)
		if strings.HasSuffix(path, ".html") {
			for _, match := range lucideAttributePattern.FindAllStringSubmatch(text, -1) {
				value := match[1]
				if strings.Contains(value, "{{") {
					for _, dynamic := range templateIconPattern.FindAllStringSubmatch(value, -1) {
						used[dynamic[1]] = true
					}
				} else {
					used[value] = true
				}
			}
			for _, match := range panelIconPattern.FindAllStringSubmatch(text, -1) {
				used[match[1]] = true
			}
		}
		if path == "web/assets/app.js" {
			for _, line := range strings.Split(text, "\n") {
				if !strings.Contains(line, "data-lucide") {
					continue
				}
				for _, match := range quotedIconPattern.FindAllStringSubmatch(line, -1) {
					if catalog[match[1]] {
						used[match[1]] = true
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan dashboard icon usage: %v", err)
	}

	var undocumented, unused []string
	for name := range used {
		if !catalog[name] {
			undocumented = append(undocumented, name)
		}
	}
	for name := range catalog {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(unused)
	if len(undocumented) != 0 {
		t.Errorf("icons used without catalog entries: %v", undocumented)
	}
	if len(unused) != 0 {
		t.Errorf("catalog icons not used by templates or app.js: %v", unused)
	}
}
