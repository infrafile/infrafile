package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func savedReaderFixture(t *testing.T) (*webFixture, string, string) {
	t.Helper()
	f, started, _ := readerFixture(t, "dynamic")
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(parent, "reads")
	if err := f.app.openOpsReadState(dir); err != nil {
		t.Fatal(err)
	}
	return f, started, dir
}

func savedBody(f *webFixture) string {
	cap := f.app.opsReader.config.Capabilities[0]
	return fmt.Sprintf(`{"action_id":%q,"target":%q}`, cap.ID, cap.Target)
}

func testCollectSaved(t *testing.T, f *webFixture) opsReadResult {
	t.Helper()
	f.app.opsReadLast = time.Time{}
	cap := f.app.opsReader.config.Capabilities[0]
	return f.app.collectAndSaveOps(context.Background(), cap.ID, cap.Target)
}

func testLoadSaved(f *webFixture) opsReadSaved {
	cap := f.app.opsReader.config.Capabilities[0]
	return f.app.savedOpsRead(context.Background(), cap.ID, cap.Target)
}

func TestSavedReadAPIRequiresSessionCSRFAndExactTargetAndDoesNotCollect(t *testing.T) {
	f, started, _ := savedReaderFixture(t)
	body := savedBody(f)
	if r := f.send(f.request("POST", "/api/ops-read/latest", body)); r.Code != 401 {
		t.Fatal(r.Code)
	}
	f.login(t)
	request := f.request("POST", "/api/ops-read/latest", body)
	request.Header.Del("X-CSRF-Token")
	if r := f.send(request); r.Code != 403 {
		t.Fatal(r.Code)
	}
	for _, bad := range []string{`{"action_id":"other","target":"Server/other"}`, strings.TrimSuffix(body, "}") + `,"command":"untrusted"}`, strings.TrimSuffix(body, "}") + `,"target":"Server/other"}`} {
		if r := f.send(f.request("POST", "/api/ops-read/latest", bad)); r.Code != 400 && r.Code != 403 {
			t.Fatal(r.Code)
		}
	}
	r := f.send(f.request("POST", "/api/ops-read/latest", body))
	var saved opsReadSaved
	json.Unmarshal(r.Body.Bytes(), &saved)
	if r.Code != 200 || saved.ErrorCode != "saved_read_missing" {
		t.Fatal(r.Code, saved.ErrorCode)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("local lookup executed reader")
	}
	if !readOps(t, f).ReadCapabilities[0].SavedRead {
		t.Fatal("saved lookup not discoverable")
	}
}

func TestSavedReadSurvivesRestartAndExpiresWithoutChangingObservationTime(t *testing.T) {
	f, started, dir := savedReaderFixture(t)
	result := testCollectSaved(t, f)
	if result.Status != "PASS" {
		t.Fatal(result.ErrorCode)
	}
	os.Remove(started)
	loaded := testLoadSaved(f)
	if loaded.Status != "PASS" || !loaded.BindingMatched || loaded.Result == nil || *loaded.Result.ObservedAt != *result.ObservedAt {
		t.Fatal(loaded.ErrorCode)
	}
	// New workbench and directory handle, with the same explicitly bound provider.
	old := f.app
	restarted := &workbench{dir: old.dir, rootInfo: old.rootInfo, opsFile: old.opsFile, opsAuthority: old.opsAuthority, opsAuthorityInfo: old.opsAuthorityInfo, opsReader: old.opsReader, now: old.now, opsReads: make(chan struct{}, 1)}
	if err := restarted.openOpsReadState(dir); err != nil {
		t.Fatal(err)
	}
	cap := old.opsReader.config.Capabilities[0]
	if v := restarted.savedOpsRead(context.Background(), cap.ID, cap.Target); v.Status != "PASS" || *v.Result.ObservedAt != *result.ObservedAt {
		t.Fatal(v.ErrorCode)
	}
	key := opsReadKey(cap.ID, cap.Target)
	path := filepath.Join(dir, key+".json")
	before, _ := os.ReadFile(path)
	f.clock = f.clock.Add(301 * time.Second)
	loaded = testLoadSaved(f)
	if loaded.Status != "STALE" || loaded.ErrorCode != "saved_read_stale" || *loaded.Result.ObservedAt != *result.ObservedAt {
		t.Fatal(loaded.ErrorCode)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("lookup rewrote capture time")
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("cache lookup executed reader")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("record not private")
	}
}

func TestFailedNewReadReplacesOldSuccessInsteadOfFallingBack(t *testing.T) {
	f, started, _ := savedReaderFixture(t)
	if r := testCollectSaved(t, f); r.Status != "PASS" {
		t.Fatal(r.ErrorCode)
	}
	os.WriteFile(started+".mode", []byte("invalid"), 0600)
	if r := testCollectSaved(t, f); r.ErrorCode != "invalid_output" {
		t.Fatal(r.ErrorCode)
	}
	loaded := testLoadSaved(f)
	if loaded.Status != "UNKNOWN" || loaded.Result == nil || loaded.Result.ErrorCode != "invalid_output" || len(loaded.Result.Sections) != 0 || loaded.Result.ObservedAt != nil {
		t.Fatal("old green survived", loaded.ErrorCode)
	}
}

func TestSavedReadIntentAndSupersededWriterCannotPublishGreen(t *testing.T) {
	f, started, _ := savedReaderFixture(t)
	if r := testCollectSaved(t, f); r.Status != "PASS" {
		t.Fatal(r.ErrorCode)
	}
	os.Remove(started)
	cap := f.app.opsReader.config.Capabilities[0]
	first, err := f.app.beginSavedRead(cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.app.beginSavedRead(cap.ID, cap.Target)
	if err != nil {
		t.Fatal(err)
	}
	loaded := testLoadSaved(f)
	if loaded.ErrorCode != "saved_read_unverified" || loaded.Result != nil {
		t.Fatal("intent revived old success")
	}
	f.app.opsReadLast = time.Time{}
	result := f.app.collectOpsID(context.Background(), cap.ID, cap.Target, first.AttemptID)
	if err = f.app.finishSavedRead(first, result); err == nil {
		t.Fatal("old writer overwrote new intent")
	}
	if err = f.app.finishSavedRead(second, result); err == nil {
		t.Fatal("different request result attributed to new intent")
	}
	if testLoadSaved(f).Result != nil {
		t.Fatal("rejected write exposed a sample")
	}
}

func TestSavedReadBindingOrSourceDriftKeepsOnlyUnverifiedHistoricalSample(t *testing.T) {
	for _, kind := range []string{"source", "program", "generation"} {
		t.Run(kind, func(t *testing.T) {
			f, started, _ := savedReaderFixture(t)
			if r := testCollectSaved(t, f); r.Status != "PASS" {
				t.Fatal(r.ErrorCode)
			}
			os.Remove(started)
			switch kind {
			case "source":
				path := filepath.Join(f.app.opsAuthority, "servers.yaml")
				raw, _ := os.ReadFile(path)
				os.WriteFile(path, append(raw, '\n'), 0600)
			case "program":
				path := f.app.opsReader.config.Executable
				raw, _ := os.ReadFile(path)
				os.WriteFile(path, append(raw, '\n'), 0700)
			case "generation":
				f.app.opsReader.digest = strings.Repeat("f", 64)
			}
			v := testLoadSaved(f)
			if v.Status != "UNKNOWN" || v.BindingMatched || v.ErrorCode != "saved_read_source_changed" || v.Result == nil {
				t.Fatal("old sample treated as current", v.ErrorCode)
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("history lookup executed provider")
			}
		})
	}
}

func TestInvalidOrReplacedSavedDirectoryRefusesCollectionBeforeSpawn(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "replaced"} {
		t.Run(kind, func(t *testing.T) {
			f, started, dir := savedReaderFixture(t)
			switch kind {
			case "mode":
				os.Chmod(dir, 0755)
			case "symlink":
				os.Rename(dir, dir+"-original")
				os.Symlink(dir+"-original", dir)
			case "replaced":
				os.Rename(dir, dir+"-original")
				os.Mkdir(dir, 0700)
			}
			v := testCollectSaved(t, f)
			if v.ErrorCode != "read_store_unavailable" || v.Status != "UNKNOWN" {
				t.Fatal(v.ErrorCode)
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("invalid store still spawned provider")
			}
		})
	}
}

func TestCorruptSavedRecordDoesNotExecuteOrExposePartialOutput(t *testing.T) {
	f, started, dir := savedReaderFixture(t)
	if r := testCollectSaved(t, f); r.Status != "PASS" {
		t.Fatal(r.ErrorCode)
	}
	os.Remove(started)
	cap := f.app.opsReader.config.Capabilities[0]
	path := filepath.Join(dir, opsReadKey(cap.ID, cap.Target)+".json")
	raw, _ := os.ReadFile(path)
	var record map[string]any
	json.Unmarshal(raw, &record)
	record["key"] = strings.Repeat("f", 64)
	bad, _ := json.Marshal(record)
	os.WriteFile(path, bad, 0600)
	v := testLoadSaved(f)
	if v.ErrorCode != "saved_read_invalid" || v.Result != nil {
		t.Fatal("corrupt record exposed fields")
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("corruption caused automatic collection")
	}
}

func TestSavedReadDirectoryAndByteBudgetsRefuseBeforeSpawning(t *testing.T) {
	for _, kind := range []string{"entries", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			f, started, dir := savedReaderFixture(t)
			if kind == "entries" {
				for i := 0; i < 255; i++ {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.json", i)), []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				file, err := os.OpenFile(filepath.Join(dir, strings.Repeat("0", 64)+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err = file.Truncate(32 << 20); err != nil {
					t.Fatal(err)
				}
				file.Close()
			}
			if v := testCollectSaved(t, f); v.ErrorCode != "read_store_unavailable" {
				t.Fatal(v.ErrorCode)
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("budget failure still ran reader")
			}
		})
	}
}

func TestSavedRecordRefusesUnsafeMetadataAndSymlinkWithoutReturningFields(t *testing.T) {
	for _, kind := range []string{"reference", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			f, started, dir := savedReaderFixture(t)
			if v := testCollectSaved(t, f); v.Status != "PASS" {
				t.Fatal(v.ErrorCode)
			}
			os.Remove(started)
			cap := f.app.opsReader.config.Capabilities[0]
			path := filepath.Join(dir, opsReadKey(cap.ID, cap.Target)+".json")
			if kind == "reference" {
				raw, _ := os.ReadFile(path)
				var record opsReadRecord
				json.Unmarshal(raw, &record)
				record.Capability.Label = "sops://fixture/reference"
				raw, _ = json.Marshal(record)
				os.WriteFile(path, raw, 0600)
			} else {
				os.Rename(path, path+".previous")
				os.Symlink(path+".previous", path)
			}
			if v := testLoadSaved(f); v.ErrorCode != "saved_read_invalid" || v.Result != nil || v.Label != "" {
				t.Fatal("unsafe stored metadata escaped")
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("invalid metadata ran provider")
			}
		})
	}
}

func TestReadStateCannotOverlapAuthorityGenerationOrActionData(t *testing.T) {
	f, _, _ := savedReaderFixture(t)
	for _, path := range []string{f.app.dir, f.app.opsAuthority, filepath.Dir(f.app.opsFile)} {
		if err := f.app.openOpsReadState(path); err == nil {
			t.Fatal("managed content reused as cache")
		}
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	one := filepath.Join(base, "one")
	os.Mkdir(one, 0700)
	if !opsStateOverlap(one, one) || !opsStateOverlap(filepath.Join(one, "nested"), one) || !opsStateOverlap(one, filepath.Join(one, "nested")) {
		t.Fatal("overlap not detected")
	}
	if opsStateOverlap(filepath.Join(base, "two"), one) {
		t.Fatal("separate state dirs treated as overlapping")
	}
}
