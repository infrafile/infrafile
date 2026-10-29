package control

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
)

func receiptFixture(t *testing.T) *Receipt {
	t.Helper()
	var p Plan
	if err := json.Unmarshal(readTestPlan(t), &p); err != nil {
		t.Fatal(err)
	}
	p.Version = "v2"
	generated, _ := time.Parse(time.RFC3339, p.GeneratedAt)
	p.ExpiresAt = generated.Add(PlanLifetime).Format(time.RFC3339)
	p.Digest = p.Sum()
	return &Receipt{Version: "v1", Kind: "ExecutionReceipt", Data: ReceiptData{ID: p.Digest, Plan: &p, Actor: "operator", AuthorizationRef: "chat:reviewed-example", CreatedAt: p.GeneratedAt, UpdatedAt: p.GeneratedAt, Phase: "prepared", Result: Result{Version: "v1", Target: p.Actions[0].Target, Status: "uncertain", Receipt: p.Digest}}}
}
func encodedReceipt(t *testing.T, r *Receipt) []byte {
	t.Helper()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReceiptRejectsUntrustedShapesAndPreservesLargeNumbers(t *testing.T) {
	r := receiptFixture(t)
	data := encodedReceipt(t, r)
	if _, err := decodeReceipt(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Receipt){
		"version":           func(r *Receipt) { r.Version = "v99" },
		"kind":              func(r *Receipt) { r.Kind = "FindingHistory" },
		"digest":            func(r *Receipt) { r.Data.Plan.Digest = strings.Repeat("0", 64) },
		"file_id":           func(r *Receipt) { r.Data.ID = strings.Repeat("0", 64) },
		"actor":             func(r *Receipt) { r.Data.Actor = "untrusted\nactor" },
		"secret":            func(r *Receipt) { r.Data.AuthorizationRef = "ghp_" + strings.Repeat("A", 36) },
		"premature_success": func(r *Receipt) { r.Data.Result.Status = "applied"; r.Data.Result.OK = true },
		"phase":             func(r *Receipt) { r.Data.Phase = "unknown" },
		"result_target":     func(r *Receipt) { r.Data.Result.Target = "Resource/different" },
		"time":              func(r *Receipt) { r.Data.UpdatedAt = "invalid" },
		"expired_start":     func(r *Receipt) { r.Data.CreatedAt = r.Data.Plan.ExpiresAt; r.Data.UpdatedAt = r.Data.CreatedAt },
		"hash":              func(r *Receipt) { r.Data.ReadbackHash = "invalid" },
		"replay":            func(r *Receipt) { r.Data.Result.Replayed = true },
	} {
		t.Run(name, func(t *testing.T) {
			r := receiptFixture(t)
			change(r)
			if _, err := decodeReceipt(context.Background(), encodedReceipt(t, r)); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
	for _, invalid := range [][]byte{
		bytes.Replace(data, []byte(`"version": "v1"`), []byte(`"version":"v1","version":"v1"`), 1),
		bytes.Replace(data, []byte(`"actor"`), []byte(`"Actor"`), 1),
		bytes.Replace(data, []byte(`"data": {`), []byte(`"unknown":true,"data": {`), 1),
		append(append([]byte{}, data...), data...), bytes.Repeat([]byte(" "), maxPlanBytes+1),
	} {
		if _, err := decodeReceipt(context.Background(), invalid); err == nil {
			t.Fatal("noncanonical or oversized receipt accepted")
		}
	}
	r = receiptFixture(t)
	p := r.Data.Plan
	p.Actions[0].Before["counter"] = json.Number("18446744073709551617")
	p.Actions[0].BeforeHash = plugin.Hash(p.Actions[0].Before)
	p.Actions[0].After, _ = plugin.Desired(p.Actions[0].Before, p.Actions[0].Binding.Target, p.Actions[0].Operation, p.Actions[0].Parameters)
	p.Digest = p.Sum()
	r.Data.ID = p.Digest
	r.Data.Result.Receipt = p.Digest
	parsed, err := decodeReceipt(context.Background(), encodedReceipt(t, r))
	if err != nil || parsed.Data.Plan.Actions[0].Before["counter"] != json.Number("18446744073709551617") {
		t.Fatal("lost integer precision", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodeReceipt(ctx, encodedReceipt(t, r)); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestReceiptReadersNeverCreateAndRejectSymlinksAndWrongIDs(t *testing.T) {
	dir := t.TempDir()
	r := receiptFixture(t)
	view, err := ListExecutions(context.Background(), dir, "")
	if err != nil || view.Present || len(view.Executions) != 0 {
		t.Fatal(view, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history")); !os.IsNotExist(err) {
		t.Fatal("reader created history")
	}
	for _, id := range []string{"../outside", strings.Repeat("A", 64), ""} {
		if _, err := ReadReceipt(context.Background(), dir, id); err == nil {
			t.Fatal("raw path accepted")
		}
	}
	root := filepath.Join(dir, "history", "executions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, r.Data.ID+".json")
	data := encodedReceipt(t, r)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadReceipt(context.Background(), dir, r.Data.ID); err != nil || got.Data.ID != r.Data.ID {
		t.Fatal(got, err)
	}
	wrong := strings.Repeat("0", 64)
	os.WriteFile(filepath.Join(root, wrong+".json"), data, 0600)
	if _, err := ReadReceipt(context.Background(), dir, wrong); err == nil {
		t.Fatal("wrong filename identity accepted")
	}
	os.Remove(filepath.Join(root, wrong+".json"))
	out := filepath.Join(t.TempDir(), "receipt.json")
	os.WriteFile(out, data, 0600)
	os.Remove(file)
	if err := os.Symlink(out, file); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := ReadReceipt(context.Background(), dir, r.Data.ID); err == nil {
		t.Fatal("followed receipt symlink")
	}
	os.Remove(file)
	os.Remove(root)
	if err := os.Symlink(filepath.Dir(out), root); err != nil {
		t.Fatal(err)
	}
	if _, err := ListExecutions(context.Background(), dir, ""); err == nil {
		t.Fatal("followed directory symlink")
	}
}

func TestReceiptStoreLockAndExternalEditsArePreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native write path intentionally rejected")
	}
	dir := t.TempDir()
	ctx := context.Background()
	r := receiptFixture(t)
	s, err := openReceiptStore(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if other, err := openReceiptStore(ctx, dir); err == nil {
		other.close()
		t.Fatal("concurrent writer acquired lock")
	}
	old, err := s.save(ctx, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "history", "executions", r.Data.ID+".json")
	external := append(old, ' ')
	if err := os.WriteFile(file, external, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, r, old); err == nil {
		t.Fatal("overwrote an external edit")
	}
	got, _ := os.ReadFile(file)
	if !bytes.Equal(got, external) {
		t.Fatal("external edit changed")
	}
	path := filepath.Dir(file)
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, r, external); err == nil {
		t.Fatal("replaced history directory accepted")
	}
}

func TestReceiptReadBudgetsRefuseWholeResult(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "history", "executions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	var last string
	for i := 0; i <= maxReceipts; i++ {
		r := receiptFixture(t)
		p := r.Data.Plan
		at, _ := time.Parse(time.RFC3339, p.GeneratedAt)
		p.GeneratedAt = at.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		p.ExpiresAt = at.Add(time.Duration(i)*time.Second + PlanLifetime).Format(time.RFC3339)
		r.Data.CreatedAt = p.GeneratedAt
		r.Data.UpdatedAt = p.GeneratedAt
		p.Digest = p.Sum()
		r.Data.ID = p.Digest
		r.Data.Result.Receipt = p.Digest
		last = filepath.Join(root, p.Digest+".json")
		if err := os.WriteFile(last, encodedReceipt(t, r), 0600); err != nil {
			t.Fatal(err)
		}
		if i == maxReceipts-1 {
			view, err := ListExecutions(context.Background(), dir, "")
			if err != nil || len(view.Executions) != maxReceipts {
				t.Fatal("valid boundary refused", err)
			}
		}
	}
	if view, err := ListExecutions(context.Background(), dir, ""); err == nil || view != nil {
		t.Fatal("returned truncated receipt list")
	}
	f, err := os.Create(last)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxPlanBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	id := strings.TrimSuffix(filepath.Base(last), ".json")
	if r, err := ReadReceipt(context.Background(), dir, id); err == nil || r != nil {
		t.Fatal("oversized receipt accepted")
	}
}
