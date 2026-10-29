package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/infrafile/infrafile/internal/change"
	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
)

func (w *workbench) changeHTTP(out http.ResponseWriter, r *http.Request, csrf string, expires time.Time) {
	name := r.URL.Path[len("/api/change/"):]
	if name != "status" && name != "preview" && name != "apply" && name != "recover" {
		webError(out, 404)
		return
	}
	if !w.allowEdit && name != "status" {
		webError(out, 403)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		webError(out, 415)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		webError(out, 413)
		return
	}
	remaining := expires.Sub(w.now())
	if remaining <= 0 {
		webError(out, 401)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(30*time.Second, remaining))
	defer cancel()
	doc, ps, err := ledger.ParseDocument(ctx, "arguments", raw)
	if err != nil || len(ps) != 0 || doc == nil || !json.Valid(raw) {
		webError(out, 400)
		return
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil || args == nil {
		webError(out, 400)
		return
	}
	w.mu.Lock()
	s, valid := w.currentSession(r)
	valid = valid && secureEqual(s.csrf, csrf)
	w.mu.Unlock()
	if !valid {
		webError(out, 401)
		return
	}
	var result any
	switch name {
	case "status":
		if len(args) != 0 {
			webError(out, 400)
			return
		}
		state := change.Result{Version: "v1", Status: "disabled", Code: "read_only"}
		if w.allowEdit {
			state, err = change.Status(ctx, w.dir)
		}
		result = map[string]any{"version": "v1", "enabled": w.allowEdit, "transaction": state}
	case "preview", "apply":
		data := raw
		digest := ""
		if name == "apply" {
			if len(args) != 2 || args["request"] == nil || json.Unmarshal(args["digest"], &digest) != nil || digest == "" {
				webError(out, 400)
				return
			}
			data = args["request"]
		}
		var request change.Request
		request, err = change.DecodeRequest(ctx, data)
		if err != nil {
			webError(out, 400)
			return
		}
		var p *change.Preview
		p, err = change.Prepare(ctx, w.dir, request, true)
		if err == nil {
			if name == "preview" {
				result, err = browserPreview(ctx, p)
			} else if !p.OK || p.Digest != digest {
				result = change.Result{Version: "v1", Status: "rejected", Code: "stale_or_invalid"}
			} else {
				w.mu.Lock()
				s, valid = w.currentSession(r)
				valid = valid && secureEqual(s.csrf, csrf)
				w.mu.Unlock()
				if !valid {
					webError(out, 401)
					return
				}
				result, err = change.Apply(ctx, w.dir, p)
			}
		}
	case "recover":
		var id string
		if len(args) != 1 || json.Unmarshal(args["id"], &id) != nil {
			webError(out, 400)
			return
		}
		result, err = change.Recover(ctx, w.dir, id)
	}
	if ctx.Err() != nil {
		webError(out, 504)
		return
	}
	if err != nil {
		webError(out, 422)
		return
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > 4<<20 || !validate.SnapshotSafe(result) {
		webError(out, 422)
		return
	}
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	out.Write(data)
}

func browserPreview(ctx context.Context, p *change.Preview) (map[string]any, error) {
	files := []any{}
	for _, f := range p.Files {
		values := []any{nil, nil}
		for i, b := range [][]byte{[]byte(f.Before), []byte(f.After)} {
			if len(b) == 0 {
				continue
			}
			doc, _, err := ledger.ParseDocument(ctx, "record", b)
			if err != nil {
				return nil, err
			}
			values[i] = withoutSecrets(doc.Value)
		}
		files = append(files, map[string]any{"key": f.Key, "path": f.Path, "existed": f.Existed, "before": values[0], "after": values[1], "before_hash": f.BeforeHash, "after_hash": f.AfterHash})
	}
	// Marshal through the same JSON value representation as read APIs before the sensitive-field guard.
	value := map[string]any{"version": "v1", "ok": p.OK, "head": p.Base.Head, "digest": p.Digest, "problems": p.Problems, "files": files}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	err = decoder.Decode(&value)
	return value, err
}
