//nolint:forbidigo // tests read this checkout only and do not dial out
package preflight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpstreamCommandsStayInTree(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	checks := map[string]string{
		"internal/cmd/runtime_options.go":      `"summary-export"`,
		"internal/cmd/runtime_options.go#mode": `"summary-mode"`,
		"internal/cmd/config.go":               `"out"`,
		"internal/cmd/config.go#usage":         `"no-usage-report"`,
		"internal/cmd/options.go#redirects":    `"max-redirects"`,
		"internal/cmd/options.go#block":        `"block-hostnames"`,
		"internal/cmd/options.go#dns":          `"dns"`,
		"internal/cmd/options.go#blacklist":    `"blacklist-ip"`,
		"internal/cmd/inspect.go":              `"execution-requirements"`,
		"internal/cmd/run.go":                  `Use:   "run"`,
		"Dockerfile":                           `ENTRYPOINT ["k6"]`,
		"lib/options.go":                       `DefaultSummaryTrendStats = []string{"avg", "min", "med", "max", "p(90)", "p(95)"}`,
		"lib/options.go#apply":                 "will overwrite all of the previous execution settings",
	}
	for name, needle := range checks {
		rel := name
		if before, _, ok := strings.Cut(name, "#"); ok {
			rel = before
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(body), needle) {
			t.Fatalf("%s no longer contains %s", rel, needle)
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			continue
		}
		body, err := os.ReadFile(match)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "ownuse/preflight") {
			t.Fatalf("workflow %s references ownuse preflight", match)
		}
	}
}

func TestPackageDoesNotStartProcessesOrRequests(t *testing.T) {
	t.Parallel()
	root := filepath.Join(repoRoot(t), "ownuse")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(body)
		for _, forbidden := range []string{"os/exec", "exec.Command", "net.Dial", "http.Get", "http.Post"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains %s", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
