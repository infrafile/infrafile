package main

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/validate"
)

const opsActionVersion = "infrafile-ops-action/v1"

var opsActionID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type opsActionRequest struct {
	Version   string `json:"version"`
	ID        string `json:"id"`
	ActionID  string `json:"action_id"`
	Target    string `json:"target"`
	Operation string `json:"operation"`
	Digest    string `json:"digest,omitempty"`
}
type opsActionResult struct {
	Version      string         `json:"version"`
	ID           string         `json:"id"`
	ActionID     string         `json:"action_id"`
	Target       string         `json:"target"`
	Operation    string         `json:"operation"`
	State        string         `json:"state"`
	ErrorCode    string         `json:"error_code"`
	Digest       string         `json:"digest"`
	ExpiresAt    *string        `json:"expires_at"`
	InvocationID string         `json:"invocation_id"`
	IdentityKind string         `json:"identity_kind,omitempty"`
	Disposition  string         `json:"disposition"`
	Fields       []opsReadField `json:"fields"`
}
type opsActionRecord struct {
	Version   string           `json:"version"`
	ID        string           `json:"id"`
	ActionID  string           `json:"action_id"`
	Target    string           `json:"target"`
	State     string           `json:"state"`
	ErrorCode string           `json:"error_code"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
	Binding   string           `json:"binding"`
	Plan      opsActionResult  `json:"plan"`
	Result    *opsActionResult `json:"result"`
}

func (w *workbench) opsActionCapabilities(o *opsOverview) []opsReadCapability {
	if w.opsActor == nil {
		return nil
	}
	reason := ""
	if o.Alignment != "matched" {
		reason = "source_changed"
	} else if !w.opsActor.valid(w.opsFile) {
		reason = "actor_changed"
	}
	result := append([]opsReadCapability(nil), w.opsActor.config.Capabilities...)
	for i := range result {
		result[i].Enabled = reason == ""
		result[i].UnavailableReason = reason
	}
	return result
}

func (w *workbench) openOpsActionState(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !outsideRepo(w.dir, absolute) || !outsideRepo(w.opsAuthority, absolute) {
		return fmt.Errorf("external action state required")
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private action directory required")
	}
	w.opsActionDir = absolute
	w.opsActionRoot = info
	return nil
}

func (w *workbench) actionRootOK() bool {
	current, err := os.Lstat(w.opsActionDir)
	return err == nil && current.IsDir() && current.Mode().Perm() == 0700 && os.SameFile(current, w.opsActionRoot)
}
func (w *workbench) readAction(id string) (opsActionRecord, error) {
	var result opsActionRecord
	if !opsActionID.MatchString(id) || !w.actionRootOK() {
		return result, fmt.Errorf("invalid receipt identity")
	}
	raw, err := opsRead(w.opsActionDir, id+".json", 64<<10)
	if err != nil {
		return result, err
	}
	if opsDecode(context.Background(), raw, &result) != nil || result.Version != opsActionVersion || result.ID != id {
		return result, fmt.Errorf("invalid receipt")
	}
	return result, nil
}
func (w *workbench) actionRecords() ([]opsActionRecord, error) {
	if !w.actionRootOK() {
		return nil, fmt.Errorf("receipt directory changed")
	}
	directory, err := os.Open(w.opsActionDir)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(1025)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > 1024 {
		return nil, fmt.Errorf("receipt budget")
	}
	var records []opsActionRecord
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("invalid receipt entry")
		}
		total += info.Size()
		if strings.HasSuffix(entry.Name(), ".json") {
			id := strings.TrimSuffix(entry.Name(), ".json")
			record, err := w.readAction(id)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
	}
	if total >= 32<<20 {
		return nil, fmt.Errorf("receipt budget")
	}
	return records, nil
}
func (w *workbench) saveAction(record opsActionRecord) error {
	if _, err := w.actionRecords(); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > 64<<10 || !opsActionID.MatchString(record.ID) {
		return fmt.Errorf("invalid receipt")
	}
	temporary, err := os.CreateTemp(w.opsActionDir, ".receipt-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(raw)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if !w.actionRootOK() {
		return fmt.Errorf("receipt directory changed")
	}
	if err = os.Rename(name, filepath.Join(w.opsActionDir, record.ID+".json")); err != nil {
		return err
	}
	directory, err := os.Open(w.opsActionDir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validateActionResult(result opsActionResult, request opsActionRequest, now time.Time) error {
	invalid := func() error { return fmt.Errorf("invalid fixed action result") }
	if result.Version != opsActionVersion || result.ID != request.ID || result.ActionID != request.ActionID || result.Target != request.Target || result.Operation != request.Operation || !opsReadCode.MatchString(result.ErrorCode) || len(result.Fields) > 32 {
		return invalid()
	}
	switch result.State {
	case "prepared", "submitting", "running", "completed", "failed", "rejected", "uncertain":
	default:
		return invalid()
	}
	if result.Digest != "" && !opsDigest.MatchString(result.Digest) {
		return invalid()
	}
	if result.InvocationID != "" && !opsActionID.MatchString(result.InvocationID) {
		return invalid()
	}
	if result.IdentityKind != "" && result.IdentityKind != "systemd_invocation" && result.IdentityKind != "docker_transaction" {
		return invalid()
	}
	if result.IdentityKind == "docker_transaction" && result.InvocationID != "" && result.InvocationID != request.ID {
		return invalid()
	}
	if result.State == "completed" && (!opsActionID.MatchString(result.InvocationID) || !opsDigest.MatchString(result.Digest)) {
		return invalid()
	}
	switch result.Disposition {
	case "", "submit_once", "join_existing", "submitted", "joined_existing":
	default:
		return invalid()
	}
	if result.State == "prepared" {
		if result.ExpiresAt == nil || !opsDigest.MatchString(result.Digest) {
			return invalid()
		}
		at, err := time.Parse(time.RFC3339Nano, *result.ExpiresAt)
		if err != nil || request.Operation == "plan" && (at.After(now.Add(time.Minute)) || !at.After(now)) {
			return invalid()
		}
	}
	for _, field := range result.Fields {
		if len(field.Label) > 256 || len(field.Value) > 1024 {
			return invalid()
		}
	}
	raw, _ := json.Marshal(result)
	var value any
	json.Unmarshal(raw, &value)
	if len(raw) > 64<<10 || opsIP.Match(raw) || bytes.Contains(raw, []byte("sops://")) || !validate.SnapshotSafe(value) {
		return invalid()
	}
	return nil
}

func (w *workbench) invokeAction(ctx context.Context, request opsActionRequest) opsActionResult {
	return w.invokeBoundAction(ctx, w.opsActor, w.opsFile, request)
}

func (w *workbench) invokeBoundAction(ctx context.Context, provider *opsReadProvider, overview string, request opsActionRequest) opsActionResult {
	result := opsActionResult{Version: opsActionVersion, ID: request.ID, ActionID: request.ActionID, Target: request.Target, Operation: request.Operation, State: "uncertain", ErrorCode: "actor_unavailable", Fields: []opsReadField{}}
	if request.Operation == "plan" {
		result.State = "rejected"
	}
	if provider == nil || !provider.valid(overview) || provider.config.Version == "infrafile-ops-reconciler/v1" && request.Operation != "status" {
		return result
	}
	seconds := 15
	for _, capability := range provider.config.Capabilities {
		if capability.ID == request.ActionID && capability.Target == request.Target {
			if request.Operation == "apply" {
				seconds = capability.TimeoutSeconds
			} else {
				seconds = min(seconds, capability.TimeoutSeconds)
			}
		}
	}
	childCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	command := provider.config.Command
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = provider.config.Directory
	raw, _ := json.Marshal(request)
	cmd.Stdin = bytes.NewReader(raw)
	output := &opsReadBuffer{limit: 64 << 10, cancel: cancel}
	diagnostic := &opsReadBuffer{limit: 8 << 10, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	err := runChild(childCtx, cmd)
	if output.exceeded || diagnostic.exceeded {
		result.ErrorCode = "output_limit"
		return result
	}
	if childCtx.Err() != nil {
		result.ErrorCode = "action_timeout"
		return result
	}
	if err != nil {
		result.ErrorCode = "actor_failed"
		return result
	}
	var observed opsActionResult
	if opsDecode(ctx, output.data, &observed) != nil || validateActionResult(observed, request, w.now()) != nil {
		result.ErrorCode = "invalid_output"
		return result
	}
	return observed
}

func actionUnresolved(state string) bool {
	return state == "submitting" || state == "running" || state == "uncertain"
}

func sameActionIdentityKind(a, b string) bool {
	if a == "" {
		a = "systemd_invocation"
	}
	if b == "" {
		b = "systemd_invocation"
	}
	return a == b
}

func rejectChangedActionOrigin(result *opsActionResult, plan opsActionResult) {
	if result.Digest != "" && result.Digest != plan.Digest || (result.IdentityKind != "" || result.State == "completed") && !sameActionIdentityKind(result.IdentityKind, plan.IdentityKind) {
		result.State, result.ErrorCode = "uncertain", "recovery_origin_mismatch"
		result.Digest, result.InvocationID, result.Fields = "", "", nil
		result.IdentityKind = plan.IdentityKind
	}
}

func (w *workbench) actionPlan(ctx context.Context, action, target string) (opsActionRecord, error) {
	w.opsActionMu.Lock()
	defer w.opsActionMu.Unlock()
	records, err := w.actionRecords()
	if err != nil {
		return opsActionRecord{}, err
	}
	for _, record := range records {
		if actionUnresolved(record.State) {
			return opsActionRecord{}, fmt.Errorf("unresolved execution")
		}
	}
	value, err := w.opsOverview(ctx)
	if err != nil || value.(opsOverview).Alignment != "matched" || !w.opsActor.valid(w.opsFile) {
		return opsActionRecord{}, fmt.Errorf("source changed")
	}
	select {
	case w.opsReads <- struct{}{}:
		defer func() { <-w.opsReads }()
	default:
		return opsActionRecord{}, fmt.Errorf("busy")
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return opsActionRecord{}, err
	}
	id := hex.EncodeToString(nonce)
	result := w.invokeAction(ctx, opsActionRequest{Version: opsActionVersion, ID: id, ActionID: action, Target: target, Operation: "plan"})
	stamp := w.now().UTC().Format(time.RFC3339Nano)
	record := opsActionRecord{Version: opsActionVersion, ID: id, ActionID: action, Target: target, State: result.State, ErrorCode: result.ErrorCode, CreatedAt: stamp, UpdatedAt: stamp, Binding: w.opsActor.digest, Plan: result}
	if err = w.saveAction(record); err != nil {
		return opsActionRecord{}, err
	}
	return record, nil
}

func (w *workbench) actionApply(ctx context.Context, id, digest string) (opsActionRecord, error) {
	w.opsActionMu.Lock()
	defer w.opsActionMu.Unlock()
	record, err := w.readAction(id)
	if err != nil {
		return record, err
	}
	if record.State != "prepared" {
		return record, nil
	}
	if record.Plan.Digest != digest || record.Binding != w.opsActor.digest || !w.opsActor.valid(w.opsFile) || record.Plan.ExpiresAt == nil {
		return record, fmt.Errorf("plan binding changed")
	}
	expiration, err := time.Parse(time.RFC3339Nano, *record.Plan.ExpiresAt)
	if err != nil || !w.now().Before(expiration) || ctx.Err() != nil {
		return record, fmt.Errorf("plan expired")
	}
	value, err := w.opsOverview(ctx)
	if err != nil || value.(opsOverview).Alignment != "matched" {
		return record, fmt.Errorf("source changed")
	}
	records, err := w.actionRecords()
	if err != nil {
		return record, err
	}
	for _, other := range records {
		if other.ID != id && actionUnresolved(other.State) {
			return record, fmt.Errorf("unresolved execution")
		}
	}
	select {
	case w.opsReads <- struct{}{}:
	default:
		return record, fmt.Errorf("busy")
	}
	claim, err := os.OpenFile(filepath.Join(w.opsActionDir, id+".dispatch"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		<-w.opsReads
		record.State = "uncertain"
		record.ErrorCode = "dispatch_claim_exists"
		w.saveAction(record)
		return record, nil
	}
	_, err = claim.WriteString(id)
	if err == nil {
		err = claim.Sync()
	}
	closeErr := claim.Close()
	if err == nil {
		err = closeErr
	}
	record.State = "submitting"
	record.ErrorCode = "submission_pending"
	record.UpdatedAt = w.now().UTC().Format(time.RFC3339Nano)
	if err == nil {
		err = w.saveAction(record)
	}
	if err != nil {
		<-w.opsReads
		return record, fmt.Errorf("write ahead receipt failed")
	}
	// Submission is now durable and independent of the originating HTTP/session.
	go func(current opsActionRecord) {
		defer func() { <-w.opsReads }()
		result := w.invokeAction(context.Background(), opsActionRequest{Version: opsActionVersion, ID: current.ID, ActionID: current.ActionID, Target: current.Target, Operation: "apply", Digest: current.Plan.Digest})
		rejectChangedActionOrigin(&result, current.Plan)
		current.Result = &result
		current.State = result.State
		current.ErrorCode = result.ErrorCode
		current.UpdatedAt = w.now().UTC().Format(time.RFC3339Nano)
		w.opsActionMu.Lock()
		defer w.opsActionMu.Unlock()
		_ = w.saveAction(current)
	}(record)
	return record, nil
}

func (w *workbench) actionReconcile(ctx context.Context, id string) (opsActionRecord, error) {
	w.opsActionMu.Lock()
	defer w.opsActionMu.Unlock()
	record, err := w.readAction(id)
	if err != nil {
		return record, err
	}
	if !actionUnresolved(record.State) {
		return record, nil
	}
	select {
	case w.opsReads <- struct{}{}:
		defer func() { <-w.opsReads }()
	default:
		return record, nil
	}
	provider, overview := w.recoveryActor(record)
	result := w.invokeBoundAction(ctx, provider, overview, opsActionRequest{Version: opsActionVersion, ID: id, ActionID: record.ActionID, Target: record.Target, Operation: "status"})
	rejectChangedActionOrigin(&result, record.Plan)
	// A later natural invocation or another plan cannot resolve this submission.
	if result.Digest != "" && result.Digest != record.Plan.Digest || record.Result != nil && record.Result.InvocationID != "" && result.InvocationID != "" && result.InvocationID != record.Result.InvocationID {
		result.State, result.ErrorCode = "uncertain", "recovery_origin_mismatch"
		result.Digest, result.InvocationID, result.Fields = "", "", nil
	}
	if result.State == "uncertain" && record.Result != nil && record.Result.InvocationID != "" {
		result.InvocationID = record.Result.InvocationID
		result.IdentityKind = record.Result.IdentityKind
	}
	if result.State == "prepared" {
		result.State = "rejected"
		result.ErrorCode = "not_submitted"
	}
	record.Result = &result
	record.State = result.State
	record.ErrorCode = result.ErrorCode
	record.UpdatedAt = w.now().UTC().Format(time.RFC3339Nano)
	return record, w.saveAction(record)
}

func (w *workbench) opsActionHTTP(out http.ResponseWriter, r *http.Request, csrf string, expires time.Time) {
	if w.opsActor == nil {
		webError(out, 404)
		return
	}
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
	operation := strings.TrimPrefix(r.URL.Path, "/api/ops-action/")
	var record opsActionRecord
	remaining := expires.Sub(w.now())
	if remaining <= 0 {
		webError(out, 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(20*time.Second, remaining))
	defer cancel()
	var action, target, id string
	if operation == "plan" || operation == "latest" {
		var args struct {
			ActionID string `json:"action_id"`
			Target   string `json:"target"`
		}
		if opsDecode(ctx, raw, &args) != nil {
			webError(out, 400)
			return
		}
		action, target = args.ActionID, args.Target
	} else if operation == "apply" {
		var args struct {
			ID       string `json:"id"`
			Digest   string `json:"digest"`
			Reviewed bool   `json:"reviewed"`
		}
		if opsDecode(ctx, raw, &args) != nil || !opsActionID.MatchString(args.ID) || !opsDigest.MatchString(args.Digest) || !args.Reviewed {
			webError(out, 400)
			return
		}
		record, err = w.readAction(args.ID)
		if err != nil {
			webError(out, 422)
			return
		}
		action, target, id = record.ActionID, record.Target, args.ID
		// Check the live session immediately before the durable dispatch transition.
		w.mu.Lock()
		session, ok := w.currentSession(r)
		ok = ok && secureEqual(session.csrf, csrf)
		w.mu.Unlock()
		if !ok {
			webError(out, 401)
			return
		}
		allowed := false
		for _, cap := range w.opsActor.config.Capabilities {
			if cap.ID == action && cap.Target == target {
				allowed = true
			}
		}
		if !allowed {
			webError(out, 403)
			return
		}
		record, err = w.actionApply(ctx, args.ID, args.Digest)
	} else if operation == "receipt" || operation == "reconcile" {
		var args struct {
			ID string `json:"id"`
		}
		if opsDecode(ctx, raw, &args) != nil || !opsActionID.MatchString(args.ID) {
			webError(out, 400)
			return
		}
		record, err = w.readAction(args.ID)
		if err != nil {
			webError(out, 422)
			return
		}
		action, target, id = record.ActionID, record.Target, args.ID
	} else {
		webError(out, 404)
		return
	}
	allowed := w.actionRecordAllowed(record)
	if operation == "plan" || operation == "latest" {
		allowed = false
		for _, cap := range w.opsActor.config.Capabilities {
			if cap.ID == action && cap.Target == target {
				allowed = true
			}
		}
	}
	if !allowed {
		webError(out, 403)
		return
	}
	switch operation {
	case "plan":
		record, err = w.actionPlan(ctx, action, target)
	case "reconcile":
		record, err = w.actionReconcile(ctx, id)
	case "receipt":
		record, err = w.readAction(id)
	case "latest":
		var records []opsActionRecord
		w.opsActionMu.Lock()
		records, err = w.actionRecords()
		w.opsActionMu.Unlock()
		for _, candidate := range records {
			if candidate.ActionID == action && candidate.Target == target && (record.ID == "" || candidate.CreatedAt > record.CreatedAt) {
				record = candidate
			}
		}
	}
	if err != nil {
		codes := map[string]string{"source changed": "source_changed", "busy": "busy", "unresolved execution": "unresolved_execution", "plan binding changed": "plan_mismatch", "plan expired": "plan_expired", "write ahead receipt failed": "receipt_write_failed"}
		code := codes[err.Error()]
		if code == "" {
			code = "receipt_unavailable"
		}
		if operation == "latest" || operation == "receipt" {
			webError(out, 422)
			return
		}
		if record.ID == "" {
			nonce := make([]byte, 16)
			rand.Read(nonce)
			record.ID = hex.EncodeToString(nonce)
		}
		record.Version = opsActionVersion
		record.ActionID = action
		record.Target = target
		record.State = "rejected"
		record.ErrorCode = code
		if code == "receipt_write_failed" || operation == "reconcile" {
			record.State = "uncertain"
		}
	}
	w.mu.Lock()
	session, ok := w.currentSession(r)
	ok = ok && secureEqual(session.csrf, csrf)
	w.mu.Unlock()
	if !ok {
		webError(out, 401)
		return
	}
	// Hashes bind local records; only non-secret result fields reach the browser.
	record.Binding = ""
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(out).Encode(record)
}
