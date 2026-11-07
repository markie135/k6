//nolint:forbidigo // tests read local fixtures only and do not dial out
package preflight

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShapeProfileIsUnexecutedUntilPlacedOutsideRepo(t *testing.T) {
	t.Parallel()
	data := readShape(t)
	profile, reasons, err := Load(data)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("shape profile reasons: %#v", reasons)
	}
	if _, err := os.Stat(profile.Script.Path); !os.IsNotExist(err) {
		t.Fatalf("shape script path must not be a local file, stat err=%v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dry-run", "--profile", shapePath(t)}, &stdout, &stderr)
	if code != exitBlocked {
		t.Fatalf("in-repo shape exit %d stderr %s", code, stderr.String())
	}
	out := decodeOutput(t, stdout.Bytes())
	if out.Status != StatusBlocked || out.Executed || out.Planned != nil {
		t.Fatalf("in-repo shape status=%s planned=%v", out.Status, out.Planned != nil)
	}
	if !hasField(out.Reasons, "profilePath") {
		t.Fatalf("missing profilePath reason: %#v", out.Reasons)
	}
}

func TestDryRunOutsideRepoDoesNotExecuteOrWrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	profilePath := writeProfile(t, root, nil)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"dry-run", "--profile", profilePath}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	out := decodeOutput(t, stdout.Bytes())
	if out.Status != StatusUnexecuted || out.Executed || out.ArtifactsWritten {
		t.Fatalf("status=%s executed=%v artifacts=%v", out.Status, out.Executed, out.ArtifactsWritten)
	}
	if out.Planned == nil || out.Planned.Commands.K6Run.Ran || out.Planned.Container.Started {
		t.Fatal("dry-run must not mark commands as ran")
	}
	if _, err := os.Stat(out.Planned.RawMetrics); !os.IsNotExist(err) {
		t.Fatalf("raw metrics path was created: %v", err)
	}
	argv := out.Planned.Commands.K6Run.Argv
	joined := strings.Join(argv, " ")
	if argv[0] != "k6" || argv[1] != "run" {
		t.Fatalf("argv start: %#v", argv[:2])
	}
	for _, forbidden := range []string{"--vus", "--duration", "--iterations", "--address"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("command contains %s: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "--summary-export") || !strings.Contains(joined, "--out") ||
		!strings.Contains(joined, "json=") || !strings.Contains(joined, "--summary-mode=full") ||
		!strings.Contains(joined, "--max-redirects") || !strings.Contains(joined, "--dns") ||
		!strings.Contains(joined, "--block-hostnames") || !strings.Contains(joined, "--rps") ||
		!strings.Contains(joined, "--no-usage-report") || !strings.Contains(joined, "--blacklist-ip") {
		t.Fatalf("missing documented flags: %s", joined)
	}
	if strings.Contains(joined, "cloud") {
		t.Fatalf("cloud invocation is out of scope: %s", joined)
	}
	if out.Report.Status != StatusUnexecuted || out.Report.Measurements.Present ||
		out.Report.ServerObservability != "missing" || len(out.Report.Hypotheses) != 0 ||
		out.Report.ThresholdsEvaluated {
		t.Fatalf("report overclaims: %#v", out.Report)
	}
	if out.Report.Improvements[0].RetestSlot != "not-scheduled" ||
		out.Report.Improvements[1].RetestSlot != "deferred" {
		t.Fatalf("retest slots: %#v", out.Report.Improvements)
	}
	if out.ExecutionBoundary.RunnerProof != "deferred" {
		t.Fatal("runner proof must stay deferred")
	}
}

func TestInspectDoesNotPrintRunCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	profilePath := writeProfile(t, root, nil)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"inspect", "--profile", profilePath}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	out := decodeOutput(t, stdout.Bytes())
	if out.Mode != "inspect" || out.Status != StatusUnexecuted || out.Planned != nil {
		t.Fatalf("inspect output: mode=%s status=%s planned=%v", out.Mode, out.Status, out.Planned != nil)
	}
}

func TestPolicyRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		edit  func(map[string]any)
		field string
	}{
		{name: "missing target", edit: func(m map[string]any) { delete(m, "target") }, field: "target.baseURL"},
		{name: "empty target", edit: func(m map[string]any) {
			m["target"] = map[string]any{"baseURL": ""}
		}, field: "target.baseURL"},
		{name: "production kind", edit: func(m map[string]any) {
			env := m["environment"].(map[string]any)
			env["kind"] = "production"
		}, field: "environment.kind"},
		{name: "prod kind", edit: func(m map[string]any) {
			env := m["environment"].(map[string]any)
			env["kind"] = "prod"
		}, field: "environment.kind"},
		{name: "staging kind", edit: func(m map[string]any) {
			env := m["environment"].(map[string]any)
			env["kind"] = "staging"
		}, field: "environment.kind"},
		{name: "loopback", edit: func(m map[string]any) {
			m["target"] = map[string]any{"baseURL": "http://127.0.0.1/"}
		}, field: "target.baseURL"},
		{name: "credentials", edit: func(m map[string]any) {
			m["target"] = map[string]any{"baseURL": "https://user:secret@app.nonprod.example.test/"}
		}, field: "target.baseURL"},
		{name: "no authorization", edit: func(m map[string]any) {
			auth := m["authorization"].(map[string]any)
			auth["explicit"] = false
			auth["reference"] = ""
		}, field: "authorization.reference"},
		{name: "wrong phase", edit: func(m map[string]any) {
			auth := m["authorization"].(map[string]any)
			auth["phase"] = "production"
		}, field: "authorization.phase"},
		{name: "hazards not acknowledged", edit: func(m map[string]any) {
			net := m["network"].(map[string]any)
			net["acknowledgeHazards"] = false
		}, field: "network.acknowledgeHazards"},
		{name: "pattern matches target", edit: func(m map[string]any) {
			net := m["network"].(map[string]any)
			net["blockHostnamePatterns"] = []any{"*example.test"}
		}, field: "network.blockHostnamePatterns"},
		{name: "bad cidr", edit: func(m map[string]any) {
			net := m["network"].(map[string]any)
			net["blacklistCIDRs"] = []any{"not-a-cidr"}
		}, field: "network.blacklistCIDRs"},
		{name: "vus above sanity", edit: func(m map[string]any) {
			limits := m["limits"].(map[string]any)
			limits["maxVUs"] = SanityMaxVUs + 1
		}, field: "limits.maxVUs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, reasons, err := Load(editShape(t, tc.edit))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !hasField(reasons, tc.field) {
				t.Fatalf("field %s not in %#v", tc.field, reasons)
			}
		})
	}
}

func TestUnknownFieldAndRunMode(t *testing.T) {
	t.Parallel()
	var doc map[string]any
	if err := json.Unmarshal(readShape(t), &doc); err != nil {
		t.Fatal(err)
	}
	doc["password"] = "nope"
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"run", "--profile", shapePath(t)}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("run mode exit %d", code)
	}
	if !strings.Contains(stderr.String(), "cannot start a test") {
		t.Fatalf("stderr %s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatal("run mode must not write a result document")
	}
	trailing := append(append([]byte{}, readShape(t)...), '\n', '{', '}', '\n')
	if _, _, err := Load(trailing); err == nil {
		t.Fatal("trailing JSON must be rejected")
	}
}

func TestClassifyAndReports(t *testing.T) {
	t.Parallel()
	profile, _, err := Load(readShape(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   OutcomeInput
		want Status
		err  bool
	}{
		{name: "unexecuted", in: OutcomeInput{}, want: StatusUnexecuted},
		{name: "blocked", in: OutcomeInput{Blocked: true}, want: StatusBlocked},
		{name: "aborted", in: OutcomeInput{Executed: true, Aborted: true}, want: StatusAborted},
		{name: "empty failed", in: OutcomeInput{Executed: true}, want: StatusFailed},
		{name: "threshold failed", in: OutcomeInput{
			Executed: true, SummaryPresent: true, MetricCount: 3, ThresholdsPassed: false,
		}, want: StatusFailed},
		{name: "passed", in: OutcomeInput{
			Executed: true, SummaryPresent: true, MetricCount: 3, ThresholdsPassed: true,
		}, want: StatusPassed},
		{name: "contradiction", in: OutcomeInput{Blocked: true, Executed: true}, err: true},
		{name: "abort before start", in: OutcomeInput{Aborted: true}, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Classify(tc.in)
			if tc.err {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %s err %v", got, err)
			}
		})
	}

	if _, err := ReportForProfile(profile, StatusPassed, nil); err == nil {
		t.Fatal("profile report must not accept passed")
	}
	rate := 0.5
	if _, err := ReportFromEvidence(profile, OutcomeInput{}, &SuppliedMetrics{
		SummaryPresent: true, MetricCount: 1, ErrorRate: &rate,
	}); err == nil {
		t.Fatal("unexecuted report must reject measurements")
	}
	empty, err := ReportFromEvidence(profile, OutcomeInput{Executed: true}, &SuppliedMetrics{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Status != StatusFailed || empty.Measurements.Present || empty.Measurements.ErrorRate != nil {
		t.Fatalf("empty result must not pass: %#v", empty.Measurements)
	}
	latency := map[string]float64{"p(95)": 12.5}
	passed, err := ReportFromEvidence(profile, OutcomeInput{
		Executed: true, SummaryPresent: true, MetricCount: 4, ThresholdsPassed: true,
	}, &SuppliedMetrics{
		SummaryPresent: true, MetricCount: 4, LatencyPercentiles: latency, ErrorRate: &rate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if passed.Status != StatusPassed || !passed.Measurements.Present ||
		passed.Measurements.LatencyPercentiles["p(95)"] != 12.5 ||
		passed.ServerObservability != "missing" || len(passed.Hypotheses) != 0 {
		t.Fatalf("passed report: %#v", passed)
	}
}

func TestPlanRejectsBadIDAndDoesNotCreateFiles(t *testing.T) {
	t.Parallel()
	profile, reasons, err := Load(readShape(t))
	if err != nil || len(reasons) != 0 {
		t.Fatalf("load err=%v reasons=%v", err, reasons)
	}
	if _, err := Plan(profile, "passed"); err == nil {
		t.Fatal("non planned id must be rejected")
	}
	planned, err := Plan(profile, "planned-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if planned.ArtifactsWritten || planned.LimitsEnforced || planned.Container.Started {
		t.Fatal("plan flags must stay false")
	}
	if _, err := os.Stat(planned.JSONSummary); !os.IsNotExist(err) {
		t.Fatal("summary file must not be created")
	}
}

func shapePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "profile-shape.json")
}

func readShape(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(shapePath(t))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func editShape(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readShape(t), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeProfile(t *testing.T, root string, edit func(map[string]any)) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readShape(t), &doc); err != nil {
		t.Fatal(err)
	}
	doc["script"] = map[string]any{
		"ref":    "network-web/perf/approved.js@v1.0.0",
		"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"path":   filepath.Join(root, "approved.js"),
	}
	doc["artifactsDir"] = filepath.Join(root, "artifacts")
	if edit != nil {
		edit(doc)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profile.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeOutput(t *testing.T, raw []byte) Output {
	t.Helper()
	var out Output
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, raw)
	}
	return out
}

func hasField(reasons []Reason, field string) bool {
	for _, reason := range reasons {
		if reason.Field == field {
			return true
		}
	}
	return false
}
