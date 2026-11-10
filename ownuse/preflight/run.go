package preflight

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	exitOK      = 0
	exitUsage   = 1
	exitBlocked = 2
)

// Output is the inspect or dry-run document written to stdout.
type Output struct {
	Tool              string            `json:"tool"`
	Mode              string            `json:"mode"`
	InstructionMarker string            `json:"instructionMarker"`
	Status            Status            `json:"status"`
	Executed          bool              `json:"executed"`
	ArtifactsWritten  bool              `json:"artifactsWritten"`
	Reasons           []Reason          `json:"reasons"`
	PlannedRunID      string            `json:"plannedRunID,omitempty"`
	Planned           *Planned          `json:"planned,omitempty"`
	Report            Report            `json:"report"`
	DoesNotProve      []string          `json:"doesNotProve"`
	ExecutionBoundary ExecutionBoundary `json:"executionBoundary"`
}

// Run is the inspect and dry-run entry point. args are the arguments after the
// program name. It never starts k6.
func Run(args []string, stdout, stderr io.Writer) int {
	mode, code, ok := parseMode(args, stderr)
	if !ok {
		return code
	}
	profilePath, err := profileFlag(args[1:])
	if err != nil {
		writef(stderr, "%s\n", err.Error())
		return exitUsage
	}
	profile, reasons, code, ok := loadChecked(profilePath, stderr)
	if !ok {
		return code
	}
	return emit(mode, profile, reasons, stdout, stderr)
}

func parseMode(args []string, stderr io.Writer) (string, int, bool) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		writef(stderr, "%s\n", usageText())
		if len(args) == 0 {
			return "", exitUsage, false
		}
		return "", exitOK, false
	}
	mode := args[0]
	if mode == "run" || mode == "execute" || mode == "load" || mode == "stress" {
		writef(stderr, "mode %q does not exist; this tool cannot start a test\n", mode)
		return "", exitUsage, false
	}
	if mode != "inspect" && mode != "dry-run" {
		writef(stderr, "unknown mode %q\n%s\n", mode, usageText())
		return "", exitUsage, false
	}
	return mode, exitOK, true
}

func loadChecked(profilePath string, stderr io.Writer) (Profile, []Reason, int, bool) {
	// The path is the operator's own profile. It is not a network fetch.
	data, err := os.ReadFile(filepath.Clean(profilePath)) //nolint:forbidigo // local profile only
	if err != nil {
		writef(stderr, "read profile: %s\n", err.Error())
		return Profile{}, nil, exitUsage, false
	}
	profile, reasons, err := Load(data)
	if err != nil {
		writef(stderr, "%s\n", err.Error())
		return Profile{}, nil, exitUsage, false
	}
	root, rootErr := findRepoRoot()
	if rootErr != nil {
		reasons = append(reasons, Reason{
			Field:   "repository",
			Message: "cannot confirm paths are outside the public fork: " + rootErr.Error(),
		})
	} else {
		reasons = append(reasons, outsideRepo(profilePath, profile, root)...)
	}
	return profile, reasons, exitOK, true
}

func emit(mode string, profile Profile, reasons []Reason, stdout, stderr io.Writer) int {
	status := StatusUnexecuted
	if len(reasons) > 0 {
		status = StatusBlocked
	}
	report, err := ReportForProfile(profile, status, reasons)
	if err != nil {
		writef(stderr, "report: %s\n", err.Error())
		return exitUsage
	}
	out := Output{
		Tool:              "ownuse-preflight",
		Mode:              mode,
		InstructionMarker: InstructionMarker,
		Status:            status,
		Executed:          false,
		ArtifactsWritten:  false,
		Reasons:           reasons,
		Report:            report,
		DoesNotProve:      DoesNotProve,
		ExecutionBoundary: boundary(),
	}
	if status == StatusUnexecuted && mode == "dry-run" {
		if code, ok := attachPlan(&out, profile, stderr); !ok {
			return code
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		writef(stderr, "encode: %s\n", err.Error())
		return exitUsage
	}
	writef(stdout, "%s\n", string(encoded))
	if status == StatusBlocked {
		return exitBlocked
	}
	return exitOK
}

func attachPlan(out *Output, profile Profile, stderr io.Writer) (int, bool) {
	runID, err := NewPlannedRunID()
	if err != nil {
		writef(stderr, "run id: %s\n", err.Error())
		return exitUsage, false
	}
	planned, err := Plan(profile, runID)
	if err != nil {
		writef(stderr, "plan: %s\n", err.Error())
		return exitUsage, false
	}
	out.PlannedRunID = runID
	out.Planned = &planned
	return exitOK, true
}

func writef(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return
	}
}

func usageText() string {
	return "usage: preflight inspect --profile <absolute-path>\n" +
		"       preflight dry-run --profile <absolute-path>\n" +
		"inspect and dry-run only. There is no run mode and no default profile."
}

func profileFlag(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--profile" || strings.TrimSpace(args[1]) == "" {
		return "", fmt.Errorf("expected --profile <path>\n%s", usageText())
	}
	return args[1], nil
}

func boundary() ExecutionBoundary {
	return ExecutionBoundary{
		Implemented: []string{
			"Load",
			"Plan",
			"Classify",
			"ReportForProfile",
			"ReportFromEvidence",
		},
		NotImplemented: []string{
			"execute",
			"agentdesk-start",
			"agentdesk-status",
			"agentdesk-results",
			"agentdesk-cancel",
		},
		RunnerProof: "deferred",
		Connector: "CLI and container entrypoint are the core. " +
			"An AgentDesk start/status/results/cancel adapter is not built.",
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd() //nolint:forbidigo // locate this checkout; no network
	if err != nil {
		return "", err
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, ".git")) //nolint:forbidigo // local git dir only
		if statErr == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no .git directory above %s", dir)
		}
		dir = parent
	}
}

func outsideRepo(profilePath string, p Profile, root string) []Reason {
	var reasons []Reason
	checks := []struct {
		field string
		path  string
	}{
		{field: "profilePath", path: profilePath},
		{field: "script.path", path: p.Script.Path},
		{field: "artifactsDir", path: p.ArtifactsDir},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.path) == "" {
			continue
		}
		resolved, err := canonical(check.path)
		if err != nil {
			reasons = append(reasons, Reason{
				Field:   check.field,
				Message: "cannot resolve path: " + err.Error(),
			})
			continue
		}
		inside, err := within(root, resolved)
		if err != nil {
			reasons = append(reasons, Reason{Field: check.field, Message: err.Error()})
			continue
		}
		if inside {
			reasons = append(reasons, Reason{
				Field: check.field,
				Message: "path is inside the public fork; keep profiles, scripts, " +
					"and artifacts outside this repository",
			})
		}
	}
	return reasons
}

func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) { //nolint:forbidigo // missing path is not a failure
			return filepath.Clean(abs), nil
		}
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func within(root, path string) (bool, error) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..", nil
}
