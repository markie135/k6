package preflight

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.k6.io/k6/v2/lib"
	"go.k6.io/k6/v2/lib/types"
)

// Sanity ceilings bound what a profile may declare. They are input limits for
// this checker. They are not a measured safe load and they are not applied to
// a live target.
const (
	SanityMaxVUs        = 50
	SanityMaxIterations = 5000
	SanityMaxRPS        = 100
	SanityMinDuration   = time.Second
	SanityMaxDuration   = 5 * time.Minute
	MaxRedirectsCeiling = 10
)

const (
	schemaVersion     = 1
	kindNonProduction = "nonproduction"
	phaseEndOfDev     = "end-of-development"
	forkURL           = "https://github.com/markie135/k6"
	upstreamParent    = "https://github.com/grafana/k6"
	baseCommit        = "4ac4745707bcf432bca392f13c4248e9924a04ed"
	instructionMarker = "7Nov 15:03 t2585u"
	entrypointDate    = "2026-11-07"
)

// ForkURL is the public fork this enablement belongs to.
const ForkURL = forkURL

// UpstreamParent is the upstream repository confirmed for this fork.
const UpstreamParent = upstreamParent

// BaseCommit is the grafana/k6 commit this enablement was added on.
const BaseCommit = baseCommit

// InstructionMarker records the own-use instruction this entry point follows.
const InstructionMarker = instructionMarker

// EntrypointDate is the date of the own-use entry point.
const EntrypointDate = entrypointDate

// Contract patterns and labels rejected without a network lookup.
//
//nolint:gochecknoglobals // fixed checks, not process state
var (
	appNamePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	scriptRefPattern = regexp.MustCompile(`^[A-Za-z0-9_./:-]+@[^@\s]+$`)
	sha256Pattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	envIDPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	metricPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	rejectedKinds    = map[string]struct{}{
		"production": {},
		"prod":       {},
		"live":       {},
	}
	rejectedEnvIDs = map[string]struct{}{
		"production": {},
		"prod":       {},
		"live":       {},
	}
)

// Profile is the per-app contract. Profiles are supplied from outside this
// public fork. This repository does not contain a filled-in target.
type Profile struct {
	SchemaVersion int           `json:"schemaVersion"`
	App           App           `json:"app"`
	Environment   Environment   `json:"environment"`
	Script        ScriptRef     `json:"script"`
	Limits        Limits        `json:"limits"`
	Thresholds    []Threshold   `json:"thresholds"`
	Target        Target        `json:"target"`
	Network       Network       `json:"network"`
	Authorization Authorization `json:"authorization"`
	ArtifactsDir  string        `json:"artifactsDir"`
}

// App identifies the web application that owns the approved script.
type App struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

// Environment is the isolated environment identity. Kind must be the exact
// value nonproduction. A matching label does not prove the host is isolated.
type Environment struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Isolation string `json:"isolation"`
}

// ScriptRef is a versioned script reference supplied by the app. Path is not
// read or executed.
type ScriptRef struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path"`
}

// Limits are the app's declared ceilings for a later run.
type Limits struct {
	MaxVUs        int    `json:"maxVUs"`
	MaxDuration   string `json:"maxDuration"`
	MaxIterations int    `json:"maxIterations"`
	MaxRPS        int    `json:"maxRPS"`
}

// Threshold is a declared k6 threshold expression. This package does not
// evaluate it and does not check that the script contains it.
type Threshold struct {
	Metric     string `json:"metric"`
	Expression string `json:"expression"`
}

// Target is required. There is no default URL.
type Target struct {
	BaseURL string `json:"baseURL"`
}

// Network records redirect and DNS inputs that a later k6 process would need
// on its command line. Recording them does not apply them.
type Network struct {
	MaxRedirects          *int     `json:"maxRedirects"`
	DNSPolicy             string   `json:"dnsPolicy"`
	AllowedHosts          []string `json:"allowedHosts"`
	BlockHostnamePatterns []string `json:"blockHostnamePatterns"`
	BlacklistCIDRs        []string `json:"blacklistCIDRs"`
	AcknowledgeHazards    bool     `json:"acknowledgeHazards"`
}

// Authorization is the separate end-of-development authorization. Explicit
// must be true. This package still does not execute a test when it is set.
type Authorization struct {
	Explicit  bool   `json:"explicit"`
	Reference string `json:"reference"`
	Phase     string `json:"phase"`
}

// Reason is one policy rejection.
type Reason struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Load decodes one JSON object and validates it. A non-nil error means the
// document is not a profile. Reasons mean the profile is blocked. Load does
// not read the script path and does not dial the target.
func Load(data []byte) (Profile, []Reason, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var profile Profile
	if err := dec.Decode(&profile); err != nil {
		return Profile{}, nil, fmt.Errorf("decode profile: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Profile{}, nil, errors.New("profile must contain one JSON object")
		}
		return Profile{}, nil, fmt.Errorf("decode profile: %w", err)
	}
	return profile, validate(profile), nil
}

func validate(p Profile) []Reason {
	reasons := make([]Reason, 0)
	reasons = append(reasons, validateIdentity(p)...)
	reasons = append(reasons, validateScript(p.Script)...)
	reasons = append(reasons, validateLimits(p.Limits)...)
	reasons = append(reasons, validateThresholds(p.Thresholds)...)
	host, hostReasons := validateTarget(p.Target)
	reasons = append(reasons, hostReasons...)
	reasons = append(reasons, validateNetwork(p.Network, host)...)
	reasons = append(reasons, validateAuthorization(p.Authorization)...)
	if strings.TrimSpace(p.ArtifactsDir) == "" {
		reasons = append(reasons, Reason{
			Field:   "artifactsDir",
			Message: "artifacts directory is required and has no default",
		})
	} else if !isAbsolute(p.ArtifactsDir) {
		reasons = append(reasons, Reason{
			Field:   "artifactsDir",
			Message: "artifacts directory must be an absolute path outside this fork",
		})
	}
	return reasons
}

func validateIdentity(p Profile) []Reason {
	var reasons []Reason
	if p.SchemaVersion != schemaVersion {
		reasons = append(reasons, Reason{
			Field:   "schemaVersion",
			Message: fmt.Sprintf("schemaVersion must be %d", schemaVersion),
		})
	}
	if !appNamePattern.MatchString(p.App.Name) {
		reasons = append(reasons, Reason{
			Field:   "app.name",
			Message: "app name must be a lowercase slug",
		})
	}
	if strings.TrimSpace(p.App.Revision) == "" || strings.ContainsAny(p.App.Revision, " \t\r\n") {
		reasons = append(reasons, Reason{
			Field:   "app.revision",
			Message: "app revision is required and must be a single token",
		})
	}
	kind := strings.ToLower(strings.TrimSpace(p.Environment.Kind))
	switch {
	case p.Environment.Kind == kindNonProduction:
	case kind == "":
		reasons = append(reasons, Reason{
			Field:   "environment.kind",
			Message: "environment kind is required and must be nonproduction",
		})
	default:
		message := "environment kind must be exactly nonproduction"
		if _, rejected := rejectedKinds[kind]; rejected {
			message = "production environments are rejected and cannot be a target"
		}
		reasons = append(reasons, Reason{Field: "environment.kind", Message: message})
	}
	if !envIDPattern.MatchString(p.Environment.ID) {
		reasons = append(reasons, Reason{
			Field:   "environment.id",
			Message: "environment id must be an explicit lowercase slug",
		})
	} else if _, rejected := rejectedEnvIDs[p.Environment.ID]; rejected {
		reasons = append(reasons, Reason{
			Field:   "environment.id",
			Message: "environment id must not be a production label",
		})
	}
	if len(strings.TrimSpace(p.Environment.Isolation)) < 8 {
		reasons = append(reasons, Reason{
			Field:   "environment.isolation",
			Message: "isolation must describe how this environment is separated",
		})
	}
	return reasons
}

func validateScript(s ScriptRef) []Reason {
	var reasons []Reason
	if !scriptRefPattern.MatchString(s.Ref) {
		reasons = append(reasons, Reason{
			Field:   "script.ref",
			Message: "script ref must be a versioned app reference such as path/approved.js@v1",
		})
	}
	if !sha256Pattern.MatchString(s.SHA256) {
		reasons = append(reasons, Reason{
			Field:   "script.sha256",
			Message: "script sha256 must be 64 lowercase hex characters",
		})
	}
	if !isAbsolute(s.Path) || !strings.HasSuffix(s.Path, ".js") {
		reasons = append(reasons, Reason{
			Field:   "script.path",
			Message: "script path must be an absolute .js path supplied by the app",
		})
	}
	return reasons
}

func validateLimits(l Limits) []Reason {
	var reasons []Reason
	if l.MaxVUs < 1 || l.MaxVUs > SanityMaxVUs {
		reasons = append(reasons, Reason{
			Field:   "limits.maxVUs",
			Message: fmt.Sprintf("maxVUs must be from 1 to %d", SanityMaxVUs),
		})
	}
	if l.MaxIterations < 1 || l.MaxIterations > SanityMaxIterations {
		reasons = append(reasons, Reason{
			Field:   "limits.maxIterations",
			Message: fmt.Sprintf("maxIterations must be from 1 to %d", SanityMaxIterations),
		})
	}
	if l.MaxRPS < 1 || l.MaxRPS > SanityMaxRPS {
		reasons = append(reasons, Reason{
			Field:   "limits.maxRPS",
			Message: fmt.Sprintf("maxRPS must be from 1 to %d", SanityMaxRPS),
		})
	}
	dur, err := time.ParseDuration(l.MaxDuration)
	if err != nil || dur < SanityMinDuration || dur > SanityMaxDuration {
		reasons = append(reasons, Reason{
			Field: "limits.maxDuration",
			Message: fmt.Sprintf(
				"maxDuration must be a Go duration from %s to %s",
				SanityMinDuration, SanityMaxDuration,
			),
		})
	}
	return reasons
}

func validateThresholds(items []Threshold) []Reason {
	if len(items) == 0 {
		return []Reason{{
			Field:   "thresholds",
			Message: "at least one declared threshold is required",
		}}
	}
	var reasons []Reason
	for i, item := range items {
		if !metricPattern.MatchString(item.Metric) || strings.TrimSpace(item.Expression) == "" ||
			strings.ContainsAny(item.Expression, "\r\n") {
			reasons = append(reasons, Reason{
				Field:   fmt.Sprintf("thresholds[%d]", i),
				Message: "threshold metric and expression must be non-empty single lines",
			})
		}
	}
	return reasons
}

func validateTarget(t Target) (string, []Reason) {
	const field = "target.baseURL"
	if strings.TrimSpace(t.BaseURL) == "" {
		return "", []Reason{{
			Field:   field,
			Message: "target baseURL is required; there is no default target",
		}}
	}
	parsed, err := url.Parse(t.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", []Reason{{
			Field:   field,
			Message: "target baseURL must be an absolute http or https URL",
		}}
	}
	if parsed.User != nil {
		return "", []Reason{{
			Field:   field,
			Message: "target baseURL must not embed credentials",
		}}
	}
	host := parsed.Hostname()
	if host == "" || isLoopbackHost(host) {
		return "", []Reason{{
			Field:   field,
			Message: "loopback and unspecified addresses are rejected",
		}}
	}
	return host, nil
}

func validateNetwork(n Network, targetHost string) []Reason {
	var reasons []Reason
	if !n.AcknowledgeHazards {
		reasons = append(reasons, Reason{
			Field: "network.acknowledgeHazards",
			Message: "redirect and DNS rebind hazards must be explicitly acknowledged; " +
				"acknowledgement does not remove them",
		})
	}
	if n.MaxRedirects == nil || *n.MaxRedirects < 0 || *n.MaxRedirects > MaxRedirectsCeiling {
		reasons = append(reasons, Reason{
			Field: "network.maxRedirects",
			Message: fmt.Sprintf(
				"maxRedirects must be set from 0 to %d; there is no hidden default",
				MaxRedirectsCeiling,
			),
		})
	}
	if !validDNSPolicy(n.DNSPolicy) {
		reasons = append(reasons, Reason{
			Field:   "network.dnsPolicy",
			Message: "dnsPolicy must be one of preferIPv4, preferIPv6, onlyIPv4, onlyIPv6, any",
		})
	}
	if len(n.AllowedHosts) == 0 {
		reasons = append(reasons, Reason{
			Field:   "network.allowedHosts",
			Message: "allowedHosts must explicitly list the target host",
		})
	}
	if targetHost != "" && !hostListed(n.AllowedHosts, targetHost) {
		reasons = append(reasons, Reason{
			Field:   "network.allowedHosts",
			Message: "target host must be listed in allowedHosts",
		})
	}
	for _, host := range n.AllowedHosts {
		if strings.Contains(host, "*") || strings.Contains(host, "/") || strings.Contains(host, " ") {
			reasons = append(reasons, Reason{
				Field:   "network.allowedHosts",
				Message: "allowedHosts entries must be explicit hosts, not patterns",
			})
			break
		}
	}
	reasons = append(reasons, validateBlockPatterns(n.BlockHostnamePatterns, n.AllowedHosts)...)
	for _, cidr := range n.BlacklistCIDRs {
		if _, err := lib.ParseCIDR(cidr); err != nil {
			reasons = append(reasons, Reason{
				Field:   "network.blacklistCIDRs",
				Message: "blacklist CIDR must parse with k6 lib.ParseCIDR: " + cidr,
			})
		}
	}
	return reasons
}

func validateBlockPatterns(patterns, allowed []string) []Reason {
	if len(patterns) == 0 {
		return []Reason{{
			Field:   "network.blockHostnamePatterns",
			Message: "at least one block-hostname pattern is required so the hazard is declared",
		}}
	}
	trie, err := types.NewHostnameTrie(patterns)
	if err != nil {
		return []Reason{{
			Field:   "network.blockHostnamePatterns",
			Message: "pattern rejected by k6 HostnameTrie: " + err.Error(),
		}}
	}
	var reasons []Reason
	for _, host := range allowed {
		if _, match := trie.Contains(host); match {
			reasons = append(reasons, Reason{
				Field: "network.blockHostnamePatterns",
				Message: "a block pattern matches allowed host " + host +
					"; k6 would block the declared host",
			})
		}
	}
	return reasons
}

func validateAuthorization(a Authorization) []Reason {
	var reasons []Reason
	if !a.Explicit || strings.TrimSpace(a.Reference) == "" {
		reasons = append(reasons, Reason{
			Field: "authorization.reference",
			Message: "separate end-of-development authorization reference is required " +
				"before a plan is unexecuted rather than blocked",
		})
	}
	if a.Phase != phaseEndOfDev {
		reasons = append(reasons, Reason{
			Field:   "authorization.phase",
			Message: "authorization phase must be exactly end-of-development",
		})
	}
	return reasons
}

func validDNSPolicy(policy string) bool {
	switch policy {
	case "preferIPv4", "preferIPv6", "onlyIPv4", "onlyIPv6", "any":
		return true
	default:
		return false
	}
}

func hostListed(hosts []string, host string) bool {
	for _, item := range hosts {
		if strings.EqualFold(item, host) {
			return true
		}
	}
	return false
}

func isLoopbackHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func isAbsolute(path string) bool {
	return path != "" && strings.HasPrefix(path, "/") && !strings.Contains(path, "\\")
}
