package preflight

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.k6.io/k6/v2/lib/types"
)

var plannedRunIDPattern = regexp.MustCompile(`^planned-[0-9a-f]{16}$`)

// DoesNotProve is the fixed list of limits of this offline checker.
//
//nolint:gochecknoglobals // immutable statements copied into every report
var DoesNotProve = []string{
	"A nonproduction label is not proof that the host is isolated from production.",
	"Hostname patterns are checked with k6 HostnameTrie. That does not prove a " +
		"future process was started with those flags, or that every other host is blocked.",
	"maxRedirects is only recorded for a later --max-redirects flag. " +
		"No connection is opened, so redirect hops are not observed.",
	"The DNS flag is parsed with k6 DNSConfig. ttl=0 disables the resolver cache " +
		"in a future process. It does not stop a name from resolving to another address.",
	"Optional CIDR blacklists are parsed with k6 lib.ParseCIDR. The target name " +
		"is not resolved, so overlap with the target address is unknown.",
	"VU, duration, and iteration ceilings are not passed as k6 CLI shortcuts " +
		"because those shortcuts replace script scenarios. The script is not read, " +
		"so its workload is not proven to stay inside the ceilings.",
	"--rps is planned at the declared ceiling. This does not prove every protocol honors it.",
	"k6 client metrics can show failures observed by the client. " +
		"They cannot prove a server root cause without server telemetry.",
	"This tool makes no load, stress, capacity, or smoke request.",
}

// Command is an argv list that this tool has not executed.
type Command struct {
	Argv           []string `json:"argv"`
	StdoutRedirect string   `json:"stdoutRedirect,omitempty"`
	Shell          string   `json:"shell"`
	Ran            bool     `json:"ran"`
}

// Commands are the existing k6 invocations a later authorized operator could
// start by hand. Ran is always false.
type Commands struct {
	K6Version Command `json:"k6Version"`
	K6Inspect Command `json:"k6Inspect"`
	K6Run     Command `json:"k6Run"`
}

// ContainerNote documents the unchanged Dockerfile. Nothing is built or started.
type ContainerNote struct {
	Dockerfile string   `json:"dockerfile"`
	Entrypoint string   `json:"entrypoint"`
	ImageTag   string   `json:"imageTag"`
	BuildArgv  []string `json:"buildArgv"`
	RunShape   string   `json:"runShape"`
	Started    bool     `json:"started"`
}

// ExecutionBoundary states which functions exist in this package.
type ExecutionBoundary struct {
	Implemented    []string `json:"implemented"`
	NotImplemented []string `json:"notImplemented"`
	RunnerProof    string   `json:"runnerProof"`
	Connector      string   `json:"connector"`
}

// Planned is a dry-run plan. It does not create artifact files.
type Planned struct {
	PlannedRunID     string        `json:"plannedRunID"`
	ArtifactsWritten bool          `json:"artifactsWritten"`
	RawMetrics       string        `json:"rawMetrics"`
	JSONSummary      string        `json:"jsonSummary"`
	HumanReport      string        `json:"humanReport"`
	Commands         Commands      `json:"commands"`
	Container        ContainerNote `json:"container"`
	LimitsEnforced   bool          `json:"limitsEnforced"`
	WorkloadNote     string        `json:"workloadNote"`
}

// NewPlannedRunID returns an id for artifact path names. It is not a k6 run id
// and no run is created.
func NewPlannedRunID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "planned-" + hex.EncodeToString(buf), nil
}

// Plan builds k6 CLI arguments for a profile that has already passed validation.
// It does not start a process.
func Plan(p Profile, runID string) (Planned, error) {
	if !plannedRunIDPattern.MatchString(runID) {
		return Planned{}, fmt.Errorf("run id %q is not a planned id", runID)
	}
	if reasons := validate(p); len(reasons) > 0 {
		return Planned{}, errors.New("profile is blocked and has no plan")
	}
	dnsFlag, err := dnsFlagValue(p.Network.DNSPolicy)
	if err != nil {
		return Planned{}, err
	}
	dir := filepath.Join(p.ArtifactsDir, runID)
	raw := filepath.Join(dir, "raw-metrics.json")
	summary := filepath.Join(dir, "summary.json")
	human := filepath.Join(dir, "human-summary.txt")
	inspectOut := filepath.Join(dir, "inspect.json")
	version := commandOf([]string{"k6", "version"}, "")
	inspect := commandOf([]string{
		"k6", "inspect", "--execution-requirements", p.Script.Path,
	}, inspectOut)
	run := runCommand(p, runID, raw, summary, human, dnsFlag)

	return Planned{
		PlannedRunID:     runID,
		ArtifactsWritten: false,
		RawMetrics:       raw,
		JSONSummary:      summary,
		HumanReport:      human,
		Commands: Commands{
			K6Version: version,
			K6Inspect: inspect,
			K6Run:     run,
		},
		Container: ContainerNote{
			Dockerfile: "Dockerfile",
			Entrypoint: "k6",
			ImageTag:   "k6-ownuse:local",
			BuildArgv:  []string{"docker", "build", "-t", "k6-ownuse:local", "."},
			RunShape: "docker run --rm --entrypoint k6 " +
				"-v <script-dir>:/scripts:ro -v <artifacts-dir>:/artifacts " +
				"k6-ownuse:local run <same flags as k6Run, with container paths>",
			Started: false,
		},
		LimitsEnforced: false,
		WorkloadNote: "VU, duration, and iteration flags are omitted because k6 " +
			"Options.Apply replaces script scenarios when those shortcuts are set. " +
			"Confirm the approved script with k6 inspect --execution-requirements " +
			"before any later run. This plan does not run inspect.",
	}, nil
}

func runCommand(p Profile, runID, raw, summary, human, dnsFlag string) Command {
	patterns := p.Network.BlockHostnamePatterns
	cidrs := p.Network.BlacklistCIDRs
	argv := make([]string, 0, 26+2*len(patterns)+2*len(cidrs)+1)
	argv = append(argv, []string{
		"k6", "run",
		"--no-usage-report",
		"--summary-mode=full",
		"--summary-export", summary,
		"--out", "json=" + raw,
		"--rps", strconv.Itoa(p.Limits.MaxRPS),
		"--max-redirects", strconv.Itoa(*p.Network.MaxRedirects),
		"--dns", dnsFlag,
		"--tag", "ownuse_env=" + p.Environment.ID,
		"--tag", "ownuse_app=" + p.App.Name,
		"--tag", "ownuse_script_sha256=" + p.Script.SHA256,
		"--env", "OWNUSE_TARGET_BASE_URL=" + p.Target.BaseURL,
		"--env", "OWNUSE_ENV_ID=" + p.Environment.ID,
		"--env", "OWNUSE_RUN_ID=" + runID,
	}...)
	for _, pattern := range patterns {
		argv = append(argv, "--block-hostnames", pattern)
	}
	for _, cidr := range cidrs {
		argv = append(argv, "--blacklist-ip", cidr)
	}
	argv = append(argv, p.Script.Path)
	return commandOf(argv, human)
}

func commandOf(argv []string, stdoutRedirect string) Command {
	return Command{
		Argv:           argv,
		StdoutRedirect: stdoutRedirect,
		Shell:          shellLine(argv, stdoutRedirect),
		Ran:            false,
	}
}

func dnsFlagValue(policy string) (string, error) {
	flag := "ttl=0,select=first,policy=" + policy
	var parsed types.DNSConfig
	if err := parsed.UnmarshalText([]byte(flag)); err != nil {
		return "", fmt.Errorf("k6 rejected dns flag: %w", err)
	}
	return flag, nil
}

func shellLine(argv []string, stdoutRedirect string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = strconv.Quote(arg)
	}
	line := strings.Join(quoted, " ")
	if stdoutRedirect != "" {
		line += " > " + strconv.Quote(stdoutRedirect)
	}
	return line
}
