package preflight

import (
	"fmt"
	"maps"
)

// Status is one result class. passed is never produced from an empty or
// unexecuted result.
type Status string

// Status values for a profile check and for a later execution record.
const (
	StatusUnexecuted Status = "unexecuted"
	StatusBlocked    Status = "blocked"
	StatusFailed     Status = "failed"
	StatusAborted    Status = "aborted"
	StatusPassed     Status = "passed"
)

// OutcomeInput is in-memory evidence for Classify. The CLI does not fill this
// from a run, because this package does not run tests.
type OutcomeInput struct {
	Blocked          bool
	Executed         bool
	Aborted          bool
	ExitError        bool
	ThresholdsPassed bool
	SummaryPresent   bool
	MetricCount      int
}

// Classify maps evidence to a status. Contradictory input returns an error.
// An executed run with no summary or no metric samples is failed, never passed.
func Classify(in OutcomeInput) (Status, error) {
	if in.Blocked && in.Executed {
		return "", fmt.Errorf("outcome cannot be both blocked and executed")
	}
	switch {
	case in.Blocked:
		return StatusBlocked, nil
	case !in.Executed && in.Aborted:
		return "", fmt.Errorf("outcome cannot be aborted before it starts")
	case !in.Executed:
		return StatusUnexecuted, nil
	case in.Aborted:
		return StatusAborted, nil
	case !in.SummaryPresent || in.MetricCount <= 0:
		return StatusFailed, nil
	case in.ExitError || !in.ThresholdsPassed:
		return StatusFailed, nil
	default:
		return StatusPassed, nil
	}
}

// Measurements holds client-observed figures. Present is false unless a caller
// supplies a completed run. This package never invents the numbers.
type Measurements struct {
	Present            bool               `json:"present"`
	LatencyPercentiles map[string]float64 `json:"latencyPercentiles"`
	ErrorRate          *float64           `json:"errorRate"`
	Throughput         *float64           `json:"throughput"`
	MetricCount        int                `json:"metricCount"`
}

// Improvement is a process gap with an explicit retest slot. It is not a
// performance measurement.
type Improvement struct {
	Priority   int    `json:"priority"`
	Item       string `json:"item"`
	RetestSlot string `json:"retestSlot"`
}

// Provenance records which fork, app, script, and environment the profile named.
type Provenance struct {
	ForkURL         string `json:"forkURL"`
	UpstreamParent  string `json:"upstreamParent"`
	BaseCommit      string `json:"baseCommit"`
	EntrypointDate  string `json:"entrypointDate"`
	AppName         string `json:"appName"`
	AppRevision     string `json:"appRevision"`
	ScriptRef       string `json:"scriptRef"`
	ScriptSHA256    string `json:"scriptSHA256"`
	EnvironmentID   string `json:"environmentID"`
	EnvironmentKind string `json:"environmentKind"`
}

// Resources repeats the declared ceilings. Enforced is false in this package.
type Resources struct {
	MaxVUs         int    `json:"maxVUs"`
	MaxDuration    string `json:"maxDuration"`
	MaxIterations  int    `json:"maxIterations"`
	MaxRPS         int    `json:"maxRPS"`
	LimitsEnforced bool   `json:"limitsEnforced"`
}

// Report is the offline report. Thresholds are the declared criteria, not
// evaluated results. ServerObservability is missing because no telemetry is read.
type Report struct {
	Status              Status        `json:"status"`
	Thresholds          []Threshold   `json:"thresholds"`
	ThresholdsEvaluated bool          `json:"thresholdsEvaluated"`
	Measurements        Measurements  `json:"measurements"`
	Provenance          Provenance    `json:"provenance"`
	Resources           Resources     `json:"resources"`
	ServerObservability string        `json:"serverObservability"`
	Evidence            []string      `json:"evidence"`
	Hypotheses          []string      `json:"hypotheses"`
	Improvements        []Improvement `json:"improvements"`
	Blockers            []Reason      `json:"blockers"`
}

// SuppliedMetrics is caller-provided evidence for a future executor. The CLI
// does not accept this input.
type SuppliedMetrics struct {
	SummaryPresent     bool
	MetricCount        int
	LatencyPercentiles map[string]float64
	ErrorRate          *float64
	Throughput         *float64
}

// ReportForProfile builds a blocked or unexecuted report. It rejects passed,
// failed, and aborted, and it rejects attached measurements.
func ReportForProfile(p Profile, status Status, reasons []Reason) (Report, error) {
	if status != StatusBlocked && status != StatusUnexecuted {
		return Report{}, fmt.Errorf("profile report cannot use status %s", status)
	}
	evidence := []string{
		"profile was checked offline",
		"k6 was not started",
		"no artifact files were written",
		"no metric samples were collected",
	}
	if status == StatusBlocked {
		evidence = append(evidence, "the profile is blocked and has no plan")
	} else {
		evidence = append(evidence, "the plan is unexecuted until a separate authorized run")
	}
	return Report{
		Status:              status,
		Thresholds:          append([]Threshold(nil), p.Thresholds...),
		ThresholdsEvaluated: false,
		Measurements: Measurements{
			Present:            false,
			LatencyPercentiles: nil,
			ErrorRate:          nil,
			Throughput:         nil,
			MetricCount:        0,
		},
		Provenance: provenance(p),
		Resources: Resources{
			MaxVUs:         p.Limits.MaxVUs,
			MaxDuration:    p.Limits.MaxDuration,
			MaxIterations:  p.Limits.MaxIterations,
			MaxRPS:         p.Limits.MaxRPS,
			LimitsEnforced: false,
		},
		ServerObservability: "missing",
		Evidence:            evidence,
		Hypotheses:          []string{},
		Improvements:        defaultImprovements(),
		Blockers:            copyReasons(reasons),
	}, nil
}

// ReportFromEvidence builds a report for a future executor's in-memory outcome.
// passed requires a summary and a positive metric count. Numbers are copied
// from the caller and are otherwise left empty.
func ReportFromEvidence(p Profile, in OutcomeInput, metrics *SuppliedMetrics) (Report, error) {
	status, err := Classify(in)
	if err != nil {
		return Report{}, err
	}
	if status == StatusBlocked || status == StatusUnexecuted {
		if metrics != nil {
			return Report{}, fmt.Errorf("measurements are not allowed for status %s", status)
		}
		return ReportForProfile(p, status, nil)
	}
	if metrics == nil {
		return Report{}, fmt.Errorf("status %s requires supplied metrics", status)
	}
	if status == StatusPassed && (!metrics.SummaryPresent || metrics.MetricCount <= 0) {
		return Report{}, fmt.Errorf("empty results cannot be passed")
	}
	present := metrics.SummaryPresent && metrics.MetricCount > 0
	var copied map[string]float64
	var errorRate, throughput *float64
	count := 0
	if present {
		copied = make(map[string]float64, len(metrics.LatencyPercentiles))
		maps.Copy(copied, metrics.LatencyPercentiles)
		errorRate = metrics.ErrorRate
		throughput = metrics.Throughput
		count = metrics.MetricCount
	}
	return Report{
		Status:              status,
		Thresholds:          append([]Threshold(nil), p.Thresholds...),
		ThresholdsEvaluated: present && (status == StatusPassed || status == StatusFailed),
		Measurements: Measurements{
			Present:            present,
			LatencyPercentiles: copied,
			ErrorRate:          errorRate,
			Throughput:         throughput,
			MetricCount:        count,
		},
		Provenance: provenance(p),
		Resources: Resources{
			MaxVUs:         p.Limits.MaxVUs,
			MaxDuration:    p.Limits.MaxDuration,
			MaxIterations:  p.Limits.MaxIterations,
			MaxRPS:         p.Limits.MaxRPS,
			LimitsEnforced: false,
		},
		ServerObservability: "missing",
		Evidence: []string{
			"figures were supplied by the caller",
			"server observability is still missing",
			"client figures do not prove server root cause",
		},
		Hypotheses:   []string{},
		Improvements: defaultImprovements(),
		Blockers:     []Reason{},
	}, nil
}

func provenance(p Profile) Provenance {
	return Provenance{
		ForkURL:         ForkURL,
		UpstreamParent:  UpstreamParent,
		BaseCommit:      BaseCommit,
		EntrypointDate:  EntrypointDate,
		AppName:         p.App.Name,
		AppRevision:     p.App.Revision,
		ScriptRef:       p.Script.Ref,
		ScriptSHA256:    p.Script.SHA256,
		EnvironmentID:   p.Environment.ID,
		EnvironmentKind: p.Environment.Kind,
	}
}

func copyReasons(reasons []Reason) []Reason {
	out := make([]Reason, len(reasons))
	copy(out, reasons)
	return out
}

func defaultImprovements() []Improvement {
	return []Improvement{
		{
			Priority: 1,
			Item: "Attach server telemetry before interpreting any later k6 client metrics. " +
				"Client errors and latency do not identify the server cause.",
			RetestSlot: "not-scheduled",
		},
		{
			Priority:   2,
			Item:       "Runner execution proof is deferred to a separate authorized task.",
			RetestSlot: "deferred",
		},
	}
}
