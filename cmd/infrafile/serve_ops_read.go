package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
)

const opsReadVersion = "infrafile-ops-read/v1"

type opsReadCapability struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	Target             string   `json:"target"`
	RelatedKeys        []string `json:"related_keys"`
	Description        string   `json:"description"`
	TimeoutSeconds     int      `json:"timeout_seconds"`
	MaxAgeSeconds      int      `json:"max_age_seconds"`
	MinIntervalSeconds int      `json:"min_interval_seconds"`
	Enabled            bool     `json:"enabled"`
	UnavailableReason  string   `json:"unavailable_reason,omitempty"`
	SavedRead          bool     `json:"saved_read,omitempty"`
}
type opsReadConfig struct {
	Executable     string              `json:"executable"`
	Version        string              `json:"version"`
	OverviewSHA256 string              `json:"overview_sha256"`
	Command        []string            `json:"command"`
	Directory      string              `json:"directory"`
	Files          map[string]string   `json:"files"`
	Capabilities   []opsReadCapability `json:"capabilities"`
}
type opsReadProvider struct {
	config       opsReadConfig
	path, digest string
	rootInfo     os.FileInfo
}
type opsReadRequest struct {
	Version   string `json:"version"`
	RequestID string `json:"request_id"`
	ActionID  string `json:"action_id"`
	Target    string `json:"target"`
}
type opsReadField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type opsReadSection struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Status string         `json:"status"`
	Fields []opsReadField `json:"fields"`
	Lines  []string       `json:"lines"`
}
type opsReadResult struct {
	Version       string           `json:"version"`
	RequestID     string           `json:"request_id"`
	ActionID      string           `json:"action_id"`
	Target        string           `json:"target"`
	Status        string           `json:"status"`
	ErrorCode     string           `json:"error_code"`
	StartedAt     string           `json:"started_at"`
	FinishedAt    string           `json:"finished_at"`
	ObservedAt    *string          `json:"observed_at"`
	DurationMS    int64            `json:"duration_ms"`
	MaxAgeSeconds int              `json:"max_age_seconds"`
	Sections      []opsReadSection `json:"sections"`
}

var opsReadID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var opsReadCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func opsDecode(ctx context.Context, data []byte, dst any) error {
	_, problems, err := ledger.ParseDocument(ctx, "ops-read.json", data)
	if err != nil || len(problems) != 0 {
		return fmt.Errorf("invalid reader document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dst) != nil || decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("invalid reader document")
	}
	return nil
}

func opsAbsoluteRead(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("absolute ordinary reader file required")
	}
	return opsRead(filepath.Dir(path), filepath.Base(path), limit)
}

func opsMatchesHash(path, expected string, limit int64) bool {
	if !opsDigest.MatchString(expected) {
		return false
	}
	data, err := opsAbsoluteRead(path, limit)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]) == expected
}

func (w *workbench) loadOpsReader(ctx context.Context, path string) (*opsReadProvider, error) {
	return w.loadOpsProvider(ctx, path, "infrafile-ops-reader/v1", 15)
}

func (w *workbench) loadOpsProvider(ctx context.Context, path, version string, maximumSeconds int) (*opsReadProvider, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 || !outsideRepo(w.dir, path) || !outsideRepo(w.opsAuthority, path) {
		return nil, fmt.Errorf("private external reader file required")
	}
	data, err := opsAbsoluteRead(path, 64<<10)
	if err != nil {
		return nil, err
	}
	var config opsReadConfig
	maximumCapabilities := 8
	if version == "infrafile-ops-reader/v1" {
		maximumCapabilities = 32
	}
	if opsDecode(ctx, data, &config) != nil || config.Version != version || len(config.Command) == 0 || len(config.Command) > 32 || len(config.Files) == 0 || len(config.Files) > 256 || len(config.Capabilities) == 0 || len(config.Capabilities) > maximumCapabilities {
		return nil, fmt.Errorf("invalid reader binding")
	}
	for _, arg := range config.Command {
		if len(arg) > 4096 {
			return nil, fmt.Errorf("reader argument budget exceeded")
		}
	}
	resolved, resolveErr := filepath.EvalSymlinks(config.Command[0])
	if !filepath.IsAbs(config.Command[0]) || resolveErr != nil || resolved != config.Executable || !opsMatchesHash(w.opsFile, config.OverviewSHA256, 2<<20) || config.Files[config.Executable] == "" {
		return nil, fmt.Errorf("reader generation differs")
	}
	executable, err := os.Lstat(config.Executable)
	if err != nil || !executable.Mode().IsRegular() || executable.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("reader executable invalid")
	}
	rootInfo, err := os.Lstat(config.Directory)
	if err != nil || !filepath.IsAbs(config.Directory) || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("reader directory invalid")
	}
	value, err := w.opsOverview(ctx)
	if err != nil {
		return nil, err
	}
	overview := value.(opsOverview)
	if overview.Alignment != "matched" {
		return nil, fmt.Errorf("reader authority differs")
	}
	keys := map[string]bool{}
	for _, s := range overview.Servers {
		keys[s.Key] = true
	}
	for _, a := range overview.Applications {
		keys[a.Key] = true
	}
	ids := map[string]bool{}
	for _, cap := range config.Capabilities {
		if !opsReadID.MatchString(cap.ID) || ids[cap.ID] || !keys[cap.Target] || len(cap.Target) < 8 || cap.Target[:7] != "Server/" || cap.TimeoutSeconds < 1 || cap.TimeoutSeconds > maximumSeconds || cap.MaxAgeSeconds < 1 || cap.MaxAgeSeconds > 600 || cap.MinIntervalSeconds < 1 || cap.MinIntervalSeconds > 300 || cap.Enabled || cap.UnavailableReason != "" || cap.SavedRead || len(cap.Label) > 256 || len(cap.Description) > 1024 || cap.RelatedKeys == nil || len(cap.RelatedKeys) > 16 {
			return nil, fmt.Errorf("invalid read capability")
		}
		ids[cap.ID] = true
		for _, key := range cap.RelatedKeys {
			if !keys[key] {
				return nil, fmt.Errorf("unknown reader relation")
			}
			if len(key) > 7 && key[:7] == "Server/" && key != cap.Target {
				return nil, fmt.Errorf("reader relation refers to another host")
			}
			if len(key) > 4 && key[:4] == "App/" && !opsCapabilityOwns(&overview, cap, key) {
				return nil, fmt.Errorf("reader relation has no deployment on target")
			}
		}
	}
	hash := sha256.Sum256(data)
	provider := &opsReadProvider{config: config, path: path, digest: hex.EncodeToString(hash[:]), rootInfo: rootInfo}
	if !provider.valid(w.opsFile) {
		return nil, fmt.Errorf("reader code changed")
	}
	return provider, nil
}

func (p *opsReadProvider) valid(overview string) bool {
	return opsMatchesHash(p.path, p.digest, 64<<10) && opsMatchesHash(overview, p.config.OverviewSHA256, 2<<20) && opsProgramValid(p.config.Executable, p.config.Command, p.config.Directory, p.config.Files, p.rootInfo)
}

func (w *workbench) opsReadCapabilities(o *opsOverview) []opsReadCapability {
	if w.opsReader == nil {
		return nil
	}
	reason := ""
	if o.Alignment != "matched" {
		reason = "source_changed"
	} else if !w.opsReader.valid(w.opsFile) {
		reason = "reader_changed"
	}
	result := append([]opsReadCapability(nil), w.opsReader.config.Capabilities...)
	for i := range result {
		result[i].Enabled = reason == ""
		result[i].UnavailableReason = reason
		result[i].SavedRead = w.opsReadDir != ""
	}
	return result
}

type opsReadBuffer struct {
	data     []byte
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *opsReadBuffer) Write(data []byte) (int, error) {
	if len(b.data)+len(data) > b.limit {
		b.exceeded = true
		b.cancel()
		return 0, fmt.Errorf("read output limit")
	}
	b.data = append(b.data, data...)
	return len(data), nil
}

func (w *workbench) collectOps(ctx context.Context, action, target string) opsReadResult {
	return w.collectOpsID(ctx, action, target, rand.Text())
}

func (w *workbench) collectOpsID(ctx context.Context, action, target, identity string) opsReadResult {
	start := w.now()
	tick := time.Now()
	request := opsReadRequest{Version: opsReadVersion, RequestID: identity, ActionID: action, Target: target}
	result := opsReadResult{Version: opsReadVersion, RequestID: request.RequestID, ActionID: action, Target: target, Status: "UNKNOWN", ErrorCode: "reader_unavailable", StartedAt: start.UTC().Format(time.RFC3339Nano), FinishedAt: start.UTC().Format(time.RFC3339Nano), MaxAgeSeconds: 300, Sections: []opsReadSection{}}
	finish := func(code, status string) opsReadResult {
		result.ErrorCode = code
		result.Status = status
		result.FinishedAt = w.now().UTC().Format(time.RFC3339Nano)
		result.DurationMS = time.Since(tick).Milliseconds()
		result.ObservedAt = nil
		result.Sections = []opsReadSection{}
		return result
	}
	if w.opsReader == nil {
		return result
	}
	value, err := w.opsOverview(ctx)
	if err != nil {
		return finish("binding_invalid", "UNKNOWN")
	}
	overview := value.(opsOverview)
	var capability *opsReadCapability
	for i := range overview.ReadCapabilities {
		c := &overview.ReadCapabilities[i]
		if c.ID == action && c.Target == target {
			capability = c
			break
		}
	}
	if capability == nil {
		return finish("target_not_supported", "UNKNOWN")
	}
	if !capability.Enabled {
		return finish(capability.UnavailableReason, "UNKNOWN")
	}
	select {
	case w.opsReads <- struct{}{}:
		defer func() { <-w.opsReads }()
	default:
		return finish("busy", "UNKNOWN")
	}
	if !w.opsReadLast.IsZero() && time.Since(w.opsReadLast) < time.Duration(capability.MinIntervalSeconds)*time.Second {
		return finish("rate_limited", "UNKNOWN")
	}
	// Recheck immediately before spawn, including source bytes and all provider code.
	value, err = w.opsOverview(ctx)
	if err != nil || value.(opsOverview).Alignment != "matched" || !w.opsReader.valid(w.opsFile) {
		return finish("source_changed", "UNKNOWN")
	}
	childCtx, cancel := context.WithTimeout(ctx, time.Duration(capability.TimeoutSeconds)*time.Second)
	defer cancel()
	w.opsReadLast = time.Now()
	command := w.opsReader.config.Command
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = w.opsReader.config.Directory
	raw, _ := json.Marshal(request)
	cmd.Stdin = bytes.NewReader(raw)
	output := &opsReadBuffer{limit: 64 << 10, cancel: cancel}
	diagnostic := &opsReadBuffer{limit: 8 << 10, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	err = runChild(childCtx, cmd)
	if output.exceeded || diagnostic.exceeded {
		return finish("output_limit", "UNKNOWN")
	}
	if childCtx.Err() != nil {
		if ctx.Err() != nil {
			return finish("cancelled", "UNKNOWN")
		}
		return finish("timeout", "FAIL")
	}
	if err != nil {
		return finish("provider_failed", "UNKNOWN")
	}
	var observed opsReadResult
	if opsDecode(ctx, output.data, &observed) != nil || validateOpsRead(observed, request, w.now()) != nil {
		return finish("invalid_output", "UNKNOWN")
	}
	value, err = w.opsOverview(ctx)
	if err != nil || value.(opsOverview).Alignment != "matched" || !w.opsReader.valid(w.opsFile) {
		return finish("source_changed", "UNKNOWN")
	}
	return observed
}

func validateOpsRead(o opsReadResult, r opsReadRequest, now time.Time) error {
	invalid := func() error { return fmt.Errorf("invalid reader result") }
	if o.Version != r.Version || o.RequestID != r.RequestID || o.ActionID != r.ActionID || o.Target != r.Target || o.MaxAgeSeconds < 1 || o.MaxAgeSeconds > 600 || o.DurationMS < 0 || o.DurationMS > 16000 || o.Sections == nil || len(o.Sections) > 8 || !opsReadCode.MatchString(o.ErrorCode) {
		return invalid()
	}
	switch o.Status {
	case "PASS", "FAIL", "UNKNOWN":
	default:
		return invalid()
	}
	start, e1 := time.Parse(time.RFC3339Nano, o.StartedAt)
	end, e2 := time.Parse(time.RFC3339Nano, o.FinishedAt)
	if e1 != nil || e2 != nil || start.After(end) || end.After(now.Add(5*time.Minute)) || now.Sub(start) > time.Minute {
		return invalid()
	}
	if o.Status == "PASS" {
		if o.ErrorCode != "ok" || o.ObservedAt == nil || len(o.Sections) == 0 {
			return invalid()
		}
		at, err := time.Parse(time.RFC3339Nano, *o.ObservedAt)
		if err != nil || at.After(now.Add(5*time.Minute)) || now.Sub(at) > time.Minute {
			return invalid()
		}
	} else if len(o.Sections) != 0 || o.ObservedAt != nil {
		return invalid()
	}
	ids := map[string]bool{}
	lines := 0
	for _, s := range o.Sections {
		if !opsReadID.MatchString(s.ID) || ids[s.ID] || len(s.Title) > 256 || s.Fields == nil || s.Lines == nil || len(s.Fields) > 32 {
			return invalid()
		}
		ids[s.ID] = true
		switch s.Status {
		case "PASS", "WARN", "FAIL", "UNKNOWN", "STALE", "PAUSED", "NA":
		default:
			return invalid()
		}
		for _, f := range s.Fields {
			if len(f.Label) > 256 || len(f.Value) > 1024 {
				return invalid()
			}
		}
		lines += len(s.Lines)
		for _, line := range s.Lines {
			if len(line) > 1024 {
				return invalid()
			}
		}
	}
	if lines > 120 {
		return invalid()
	}
	raw, _ := json.Marshal(o)
	var safe any
	if json.Unmarshal(raw, &safe) != nil {
		return invalid()
	}
	if len(raw) > 64<<10 || opsIP.Match(raw) || bytes.Contains(raw, []byte("sops://")) || !validate.SnapshotSafe(safe) {
		return invalid()
	}
	return nil
}

func (w *workbench) opsReadHTTP(out http.ResponseWriter, r *http.Request, csrf string, expires time.Time) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		webError(out, 415)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2049))
	if err != nil || len(raw) > 2048 {
		webError(out, 413)
		return
	}
	var args struct {
		ActionID string `json:"action_id"`
		Target   string `json:"target"`
	}
	if opsDecode(r.Context(), raw, &args) != nil || !opsReadID.MatchString(args.ActionID) || len(args.Target) > 262 {
		webError(out, 400)
		return
	}
	if w.opsReader == nil {
		webError(out, 404)
		return
	}
	allowed := false
	for _, c := range w.opsReader.config.Capabilities {
		if c.ID == args.ActionID && c.Target == args.Target {
			allowed = true
		}
	}
	if !allowed {
		webError(out, 403)
		return
	}
	remaining := expires.Sub(w.now())
	if remaining <= 0 {
		webError(out, 401)
		return
	}
	if r.URL.Path == "/api/ops-read/latest" {
		if w.opsReadDir == "" {
			webError(out, 404)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), min(5*time.Second, remaining))
		defer cancel()
		result := w.savedOpsRead(ctx, args.ActionID, args.Target)
		w.mu.Lock()
		session, ok := w.currentSession(r)
		ok = ok && secureEqual(session.csrf, csrf)
		w.mu.Unlock()
		if !ok {
			webError(out, 401)
			return
		}
		if ctx.Err() != nil {
			return
		}
		out.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(out).Encode(result)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(20*time.Second, remaining))
	defer cancel()
	result := w.collectAndSaveOps(ctx, args.ActionID, args.Target)
	w.mu.Lock()
	session, ok := w.currentSession(r)
	ok = ok && secureEqual(session.csrf, csrf)
	w.mu.Unlock()
	if !ok {
		webError(out, 401)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(out).Encode(result)
}
