package main

import (
	"context"
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
	"reflect"
	"time"
)

type opsRefreshConfig struct {
	Version        string            `json:"version"`
	Executable     string            `json:"executable"`
	Command        []string          `json:"command"`
	Directory      string            `json:"directory"`
	Files          map[string]string `json:"files"`
	GenerationRoot string            `json:"generation_root"`
	LockFile       string            `json:"lock_file"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Reader         bool              `json:"reader"`
	Actions        bool              `json:"actions"`
}
type opsRefreshProvider struct {
	config                                opsRefreshConfig
	path, digest                          string
	programRoot, generationRoot, lockRoot os.FileInfo
}
type opsRefreshState struct {
	Enabled        bool   `json:"enabled"`
	Reason         string `json:"reason,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}
type opsRefreshResult struct {
	Version    string `json:"version"`
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code"`
	Generation string `json:"generation,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`
}
type opsGenerationOutput struct {
	Version        string     `json:"version"`
	Package        string     `json:"package"`
	OverviewSHA256 string     `json:"overview_sha256"`
	Summary        opsSummary `json:"summary"`
}

func opsProgramValid(executable string, command []string, directory string, files map[string]string, root os.FileInfo) bool {
	if len(command) == 0 || len(command) > 32 || len(files) == 0 || len(files) > 256 || files[executable] == "" {
		return false
	}
	for _, arg := range command {
		if len(arg) > 4096 {
			return false
		}
	}
	resolved, err := filepath.EvalSymlinks(command[0])
	if err != nil || !filepath.IsAbs(command[0]) || resolved != executable {
		return false
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, root) {
		return false
	}
	info, err = os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return false
	}
	for path, hash := range files {
		limit := int64(4 << 20)
		if path == executable {
			limit = 64 << 20
		}
		if !opsMatchesHash(path, hash, limit) {
			return false
		}
	}
	return true
}
func (p *opsRefreshProvider) valid() bool {
	if !opsMatchesHash(p.path, p.digest, 64<<10) || !opsProgramValid(p.config.Executable, p.config.Command, p.config.Directory, p.config.Files, p.programRoot) {
		return false
	}
	for path, expected := range map[string]os.FileInfo{p.config.GenerationRoot: p.generationRoot, filepath.Dir(p.config.LockFile): p.lockRoot} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || !os.SameFile(info, expected) {
			return false
		}
	}
	return true
}
func (w *workbench) loadOpsRefresher(ctx context.Context, path string) (*opsRefreshProvider, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 || !outsideRepo(w.dir, path) || !outsideRepo(w.opsAuthority, path) {
		return nil, fmt.Errorf("private external generation binding required")
	}
	raw, err := opsAbsoluteRead(path, 64<<10)
	if err != nil {
		return nil, err
	}
	var c opsRefreshConfig
	if opsDecode(ctx, raw, &c) != nil || c.Version != "infrafile-ops-refresh/v1" || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 25 || c.Reader != (w.opsReader != nil) || c.Actions != (w.opsActor != nil) {
		return nil, fmt.Errorf("invalid generation binding")
	}
	for _, p := range []string{c.GenerationRoot, c.LockFile, c.Directory} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, fmt.Errorf("ordinary absolute paths required")
		}
	}
	resolved, err := filepath.EvalSymlinks(c.GenerationRoot)
	if err != nil || resolved != c.GenerationRoot || !outsideRepo(w.dir, c.GenerationRoot) || !outsideRepo(w.opsAuthority, c.GenerationRoot) || !outsideRepo(w.dir, c.LockFile) || !outsideRepo(w.opsAuthority, c.LockFile) {
		return nil, fmt.Errorf("external generation paths required")
	}
	root, e1 := os.Lstat(c.GenerationRoot)
	program, e2 := os.Lstat(c.Directory)
	lock, e3 := os.Lstat(filepath.Dir(c.LockFile))
	if e1 != nil || e2 != nil || e3 != nil || !root.IsDir() || root.Mode().Perm() != 0700 || !program.IsDir() || !lock.IsDir() || lock.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid generation roots")
	}
	hash := sha256.Sum256(raw)
	p := &opsRefreshProvider{config: c, path: path, digest: hex.EncodeToString(hash[:]), programRoot: program, generationRoot: root, lockRoot: lock}
	if !p.valid() {
		return nil, fmt.Errorf("generation program changed")
	}
	return p, nil
}
func (w *workbench) opsRefreshState() *opsRefreshState {
	if w.opsRefresher == nil {
		return nil
	}
	state := &opsRefreshState{Enabled: true, TimeoutSeconds: w.opsRefresher.config.TimeoutSeconds}
	if !w.opsRefresher.valid() {
		state.Enabled = false
		state.Reason = "generator_changed"
	}
	current, err := os.Lstat(w.opsAuthority)
	if err != nil || !os.SameFile(current, w.opsAuthorityInfo) {
		state.Enabled = false
		state.Reason = "authority_changed"
	}
	return state
}
func (w *workbench) pendingGenerationAction() string {
	if w.opsActor == nil {
		return ""
	}
	records, err := w.actionRecords()
	if err != nil {
		return "receipt_unavailable"
	}
	for _, r := range records {
		switch r.State {
		case "completed", "failed", "rejected":
			continue
		case "prepared":
			if r.Plan.ExpiresAt == nil {
				return "prepared_plan"
			}
			end, err := time.Parse(time.RFC3339Nano, *r.Plan.ExpiresAt)
			if err != nil || w.now().Before(end) {
				return "prepared_plan"
			}
		default:
			return "unresolved_execution"
		}
	}
	return ""
}
func (w *workbench) refreshOps(ctx context.Context, sessionCheck func() bool) opsRefreshResult {
	result := opsRefreshResult{Version: "infrafile-ops-refresh-result/v1", Status: "UNKNOWN", ErrorCode: "refresh_unavailable"}
	fail := func(code string) opsRefreshResult { result.ErrorCode = code; return result }
	if !w.opsGenerationMu.TryLock() {
		return fail("busy")
	}
	defer w.opsGenerationMu.Unlock()
	if w.opsRefresher == nil {
		return result
	}
	state := w.opsRefreshState()
	if !state.Enabled {
		return fail(state.Reason)
	}
	select {
	case w.opsReads <- struct{}{}:
		defer func() { <-w.opsReads }()
	default:
		return fail("busy")
	}
	release, err := opsGenerationLock(w.opsRefresher.config.LockFile)
	if err != nil {
		return fail("busy")
	}
	defer release()
	if code := w.pendingGenerationAction(); code != "" {
		return fail(code)
	}
	if ctx.Err() != nil || !sessionCheck() {
		return fail("cancelled")
	}
	childCtx, cancel := context.WithTimeout(ctx, time.Duration(w.opsRefresher.config.TimeoutSeconds)*time.Second)
	defer cancel()
	command := w.opsRefresher.config.Command
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = w.opsRefresher.config.Directory
	output := &opsReadBuffer{limit: 4096, cancel: cancel}
	diagnostic := &opsReadBuffer{limit: 8192, cancel: cancel}
	cmd.Stdin = nil
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	err = runChild(childCtx, cmd)
	if output.exceeded || diagnostic.exceeded {
		return fail("output_limit")
	}
	if childCtx.Err() != nil {
		if ctx.Err() != nil {
			return fail("cancelled")
		}
		return fail("timeout")
	}
	if err != nil {
		return fail("generation_failed")
	}
	var generated opsGenerationOutput
	if opsDecode(ctx, output.data, &generated) != nil || generated.Version != "infrafile-ops-generation/v1" || !opsActionID.MatchString(filepath.Base(generated.Package)) || filepath.Dir(generated.Package) != w.opsRefresher.config.GenerationRoot {
		return fail("invalid_generation")
	}
	data, err := opsRead(w.opsRefresher.config.GenerationRoot, filepath.Base(generated.Package)+"/ops.json", 2<<20)
	if err != nil {
		return fail("invalid_generation")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != generated.OverviewSHA256 {
		return fail("invalid_generation")
	}
	dir := filepath.Join(generated.Package, "bundle/candidate")
	if _, err = opsRead(w.opsRefresher.config.GenerationRoot, filepath.Base(generated.Package)+"/bundle/candidate/infrafile.yaml", 2<<20); err != nil {
		return fail("invalid_generation")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return fail("invalid_generation")
	}
	candidate := &workbench{dir: dir, rootInfo: info, opsFile: filepath.Join(generated.Package, "ops.json"), opsAuthority: w.opsAuthority, opsAuthorityInfo: w.opsAuthorityInfo, now: w.now, opsReads: w.opsReads}
	value, err := candidate.opsOverview(ctx)
	if err != nil || value.(opsOverview).Alignment != "matched" || value.(opsOverview).Summary != generated.Summary {
		return fail("generation_validation_failed")
	}
	if w.opsRefresher.config.Reader {
		candidate.opsReader, err = candidate.loadOpsReader(ctx, filepath.Join(generated.Package, "reader.json"))
		if err != nil {
			return fail("generation_validation_failed")
		}
	}
	if w.opsRefresher.config.Actions {
		candidate.opsActor, err = candidate.loadOpsProvider(ctx, filepath.Join(generated.Package, "actor.json"), "infrafile-ops-actor/v1", 45)
		if err != nil || !reflect.DeepEqual(candidate.opsActor.config.Capabilities, w.opsActor.config.Capabilities) {
			return fail("action_scope_changed")
		}
	}
	if code := w.pendingGenerationAction(); code != "" {
		return fail(code)
	}
	if !w.opsRefresher.valid() || ctx.Err() != nil || !sessionCheck() {
		return fail("cancelled")
	}
	value, err = candidate.opsOverview(ctx)
	if err != nil || value.(opsOverview).Alignment != "matched" {
		return fail("source_changed")
	}
	// No mutation before every candidate/source/program/receipt/session gate passes.
	// In-flight submitted jobs hold opsReads and prevent entering this transition.
	w.dir, w.rootInfo, w.opsFile, w.opsReader, w.opsActor = candidate.dir, candidate.rootInfo, candidate.opsFile, candidate.opsReader, candidate.opsActor
	result.Status, result.ErrorCode, result.Generation, result.CapturedAt = "PASS", "ok", generated.OverviewSHA256, value.(opsOverview).CapturedAt
	return result
}
func (w *workbench) opsRefreshHTTP(out http.ResponseWriter, r *http.Request, csrf string, expires time.Time) {
	if w.opsRefresher == nil {
		webError(out, 404)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		webError(out, 415)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 129))
	if err != nil || len(raw) > 128 {
		webError(out, 413)
		return
	}
	var args struct{}
	if opsDecode(r.Context(), raw, &args) != nil {
		webError(out, 400)
		return
	}
	remaining := expires.Sub(w.now())
	if remaining <= 0 {
		webError(out, 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(30*time.Second, remaining))
	defer cancel()
	check := func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		s, ok := w.currentSession(r)
		return ok && secureEqual(s.csrf, csrf)
	}
	result := w.refreshOps(ctx, check)
	if !check() {
		webError(out, 401)
		return
	}
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(out).Encode(result)
}
