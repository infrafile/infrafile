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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/infrafile/infrafile/internal/validate"
)

const opsReadSavedVersion = "infrafile-ops-read-saved/v1"

type opsReadRecord struct {
	Version    string            `json:"version"`
	Key        string            `json:"key"`
	AttemptID  string            `json:"attempt_id"`
	Binding    string            `json:"binding"`
	Capability opsReadCapability `json:"capability"`
	SavedAt    string            `json:"saved_at"`
	Phase      string            `json:"phase"`
	Result     *opsReadResult    `json:"result"`
}

type opsReadSaved struct {
	Version        string         `json:"version"`
	Status         string         `json:"status"`
	ErrorCode      string         `json:"error_code"`
	BindingMatched bool           `json:"binding_matched"`
	SavedAt        string         `json:"saved_at,omitempty"`
	Label          string         `json:"label,omitempty"`
	Result         *opsReadResult `json:"result,omitempty"`
}

var opsReadAttempt = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func savedReadSafe(raw []byte) bool {
	var value any
	return json.Unmarshal(raw, &value) == nil && !opsIP.Match(raw) && !bytes.Contains(raw, []byte("sops://")) && validate.SnapshotSafe(value)
}

func opsReadKey(action, target string) string {
	raw, _ := json.Marshal([]string{action, target})
	value := sha256.Sum256(raw)
	return hex.EncodeToString(value[:])
}

func opsStateOverlap(first, second string) bool {
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
		return filepath.Join(parent, filepath.Base(absolute)), err
	}
	a, err1 := canonical(first)
	b, err2 := canonical(second)
	if err1 != nil || err2 != nil {
		return true
	}
	inside := func(root, path string) bool {
		rel, err := filepath.Rel(root, path)
		return err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return inside(a, b) || inside(b, a)
}

func (w *workbench) openOpsReadState(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil || opsStateOverlap(w.dir, absolute) || opsStateOverlap(w.opsAuthority, absolute) || opsStateOverlap(filepath.Dir(w.opsFile), absolute) {
		return fmt.Errorf("external read state required")
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != absolute {
		return fmt.Errorf("ordinary read state required")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !opsPrivateOwner(info) {
		return fmt.Errorf("private read state required")
	}
	w.opsReadDir, w.opsReadRoot = absolute, info
	release, err := w.readStateLease()
	if err == nil {
		release()
	}
	return err
}

func (w *workbench) readStateOK() bool {
	info, err := os.Lstat(w.opsReadDir)
	return err == nil && info.IsDir() && info.Mode().Perm() == 0700 && opsPrivateOwner(info) && os.SameFile(info, w.opsReadRoot)
}

func (w *workbench) readStateLease() (func(), error) {
	if !w.readStateOK() {
		return nil, fmt.Errorf("read state changed")
	}
	release, err := opsGenerationLock(filepath.Join(w.opsReadDir, ".lock"))
	if err != nil {
		return nil, err
	}
	if !w.readStateOK() {
		release()
		return nil, fmt.Errorf("read state changed")
	}
	return release, nil
}

func (w *workbench) readRecord(key string) (*opsReadRecord, error) {
	if !opsDigest.MatchString(key) || !w.readStateOK() {
		return nil, fmt.Errorf("invalid read key")
	}
	info, err := os.Lstat(filepath.Join(w.opsReadDir, key+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !opsPrivateOwner(info) {
		return nil, fmt.Errorf("invalid read record")
	}
	raw, err := opsRead(w.opsReadDir, key+".json", 128<<10)
	if err != nil || !savedReadSafe(raw) {
		return nil, fmt.Errorf("invalid saved read")
	}
	var record opsReadRecord
	if opsDecode(context.Background(), raw, &record) != nil || record.Version != opsReadSavedVersion || record.Key != key || !opsReadAttempt.MatchString(record.AttemptID) || !opsDigest.MatchString(record.Binding) || record.Key != opsReadKey(record.Capability.ID, record.Capability.Target) || len(record.Capability.Label) > 256 || record.Capability.MaxAgeSeconds < 1 || record.Capability.MaxAgeSeconds > 600 {
		return nil, fmt.Errorf("invalid read record")
	}
	at, err := time.Parse(time.RFC3339Nano, record.SavedAt)
	if err != nil || at.After(w.now().Add(5*time.Minute)) {
		return nil, fmt.Errorf("invalid read time")
	}
	if record.Phase == "intent" && record.Result == nil {
		return &record, nil
	}
	if record.Phase != "finished" || record.Result == nil {
		return nil, fmt.Errorf("invalid read phase")
	}
	r := record.Result
	request := opsReadRequest{Version: opsReadVersion, RequestID: record.AttemptID, ActionID: record.Capability.ID, Target: record.Capability.Target}
	// Validate against the actual save time, never restamp an old observation.
	if validateOpsRead(*r, request, at) != nil {
		return nil, fmt.Errorf("invalid saved read")
	}
	return &record, nil
}

func (w *workbench) saveReadRecord(record opsReadRecord) error {
	if !w.readStateOK() {
		return fmt.Errorf("read state changed")
	}
	dir, err := os.Open(w.opsReadDir)
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(257)
	dir.Close()
	if err != nil && err != io.EOF || len(entries) > 256 {
		return fmt.Errorf("read state budget")
	}
	var total int64
	exists := false
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !opsPrivateOwner(info) {
			return fmt.Errorf("invalid read state entry")
		}
		if entry.Name() != ".lock" && !strings.HasPrefix(entry.Name(), ".read-") && !(strings.HasSuffix(entry.Name(), ".json") && opsDigest.MatchString(strings.TrimSuffix(entry.Name(), ".json"))) {
			return fmt.Errorf("unknown read state entry")
		}
		total += info.Size()
		if entry.Name() == record.Key+".json" {
			exists = true
		}
	}
	if !exists && len(entries) >= 256 {
		return fmt.Errorf("read state budget")
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > 128<<10 || total+int64(len(raw)) > 32<<20 || !savedReadSafe(raw) {
		return fmt.Errorf("read state budget")
	}
	temporary, err := os.CreateTemp(w.opsReadDir, ".read-")
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
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if !w.readStateOK() {
		return fmt.Errorf("read state changed")
	}
	if err = os.Rename(name, filepath.Join(w.opsReadDir, record.Key+".json")); err != nil {
		return err
	}
	directory, err := os.Open(w.opsReadDir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (w *workbench) beginSavedRead(action, target string) (*opsReadRecord, error) {
	release, err := w.readStateLease()
	if err != nil {
		return nil, err
	}
	defer release()
	var capability *opsReadCapability
	for _, c := range w.opsReader.config.Capabilities {
		if c.ID == action && c.Target == target {
			copy := c
			capability = &copy
			break
		}
	}
	if capability == nil {
		return nil, fmt.Errorf("unknown read target")
	}
	record := &opsReadRecord{Version: opsReadSavedVersion, Key: opsReadKey(action, target), AttemptID: rand.Text(), Binding: w.opsReader.digest, Capability: *capability, SavedAt: w.now().UTC().Format(time.RFC3339Nano), Phase: "intent"}
	if err = w.saveReadRecord(*record); err != nil {
		return nil, err
	}
	return record, nil
}

func (w *workbench) finishSavedRead(record *opsReadRecord, result opsReadResult) error {
	release, err := w.readStateLease()
	if err != nil {
		return err
	}
	defer release()
	current, err := w.readRecord(record.Key)
	if err != nil || current == nil || current.AttemptID != record.AttemptID || current.Binding != record.Binding {
		return fmt.Errorf("read attempt changed")
	}
	request := opsReadRequest{Version: opsReadVersion, RequestID: record.AttemptID, ActionID: record.Capability.ID, Target: record.Capability.Target}
	if validateOpsRead(result, request, w.now()) != nil {
		return fmt.Errorf("invalid read result")
	}
	record.Result = &result
	record.Phase = "finished"
	record.SavedAt = w.now().UTC().Format(time.RFC3339Nano)
	return w.saveReadRecord(*record)
}

func (w *workbench) collectAndSaveOps(ctx context.Context, action, target string) opsReadResult {
	if w.opsReadDir == "" {
		return w.collectOps(ctx, action, target)
	}
	failed := func(code string) opsReadResult {
		stamp := w.now().UTC().Format(time.RFC3339Nano)
		return opsReadResult{Version: opsReadVersion, RequestID: rand.Text(), ActionID: action, Target: target, Status: "UNKNOWN", ErrorCode: code, StartedAt: stamp, FinishedAt: stamp, MaxAgeSeconds: 300, Sections: []opsReadSection{}}
	}
	record, err := w.beginSavedRead(action, target)
	if err != nil {
		return failed("read_store_unavailable")
	}
	result := w.collectOpsID(ctx, action, target, record.AttemptID)
	if w.finishSavedRead(record, result) != nil {
		return failed("read_save_failed")
	}
	return result
}

func (w *workbench) savedOpsRead(ctx context.Context, action, target string) opsReadSaved {
	value := opsReadSaved{Version: opsReadSavedVersion, Status: "UNKNOWN", ErrorCode: "read_store_unavailable"}
	release, err := w.readStateLease()
	if err != nil {
		return value
	}
	defer release()
	record, err := w.readRecord(opsReadKey(action, target))
	if err != nil {
		value.ErrorCode = "saved_read_invalid"
		return value
	}
	if record == nil {
		value.ErrorCode = "saved_read_missing"
		return value
	}
	value.SavedAt, value.Label = record.SavedAt, record.Capability.Label
	if record.Phase == "intent" {
		value.ErrorCode = "saved_read_unverified"
		return value
	}
	value.Result = record.Result
	// Frozen samples remain inspectable but are never presented as current green.
	overview, err := w.opsOverview(ctx)
	if err != nil || overview.(opsOverview).Alignment != "matched" || w.opsReader == nil || !w.opsReader.valid(w.opsFile) || record.Binding != w.opsReader.digest {
		value.ErrorCode = "saved_read_source_changed"
		return value
	}
	value.BindingMatched = true
	value.ErrorCode = record.Result.ErrorCode
	value.Status = record.Result.Status
	if value.Status == "PASS" {
		at, _ := time.Parse(time.RFC3339Nano, *record.Result.ObservedAt)
		maximum := min(record.Result.MaxAgeSeconds, record.Capability.MaxAgeSeconds)
		copy := *record.Result
		copy.MaxAgeSeconds = maximum
		value.Result = &copy
		if w.now().Sub(at) > time.Duration(maximum)*time.Second || at.After(w.now().Add(5*time.Minute)) {
			value.Status = "STALE"
			value.ErrorCode = "saved_read_stale"
		}
	}
	return value
}
