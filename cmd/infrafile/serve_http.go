package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/infrafile/infrafile/internal/ledger"
	"github.com/infrafile/infrafile/internal/validate"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed web/*
var workbenchFiles embed.FS
var workbenchTemplate = template.Must(template.ParseFS(workbenchFiles, "web/serve.html"))

const sessionCookie = "infrafile_session"
const pairCookie = "infrafile_pair"

type webSession struct {
	csrf    string
	expires time.Time
}

type workbench struct {
	dir                       string
	rootInfo                  os.FileInfo
	address                   serveAddress
	sessionTTL                time.Duration
	now                       func() time.Time
	publish                   func(string) error
	queries                   map[string]*jsonschema.Schema
	requests                  chan struct{}
	mu                        sync.Mutex
	pair, pairCSRF            string
	pairExpires, attemptReset time.Time
	sessions                  map[string]webSession
	attempts                  int
}

func newWorkbench(ctx context.Context, dir string, address serveAddress, ttl time.Duration, now func() time.Time, publish func(string) error) (*workbench, error) {
	dir, err := filepath.Abs(dir)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return nil, fmt.Errorf("无法固定数据仓目录")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("数据仓必须是普通目录")
	}
	if _, _, err := ledger.Load(ctx, dir); err != nil {
		return nil, fmt.Errorf("无法读取数据仓")
	}
	definitions, err := queryTools()
	if err != nil {
		return nil, err
	}
	w := &workbench{dir: dir, rootInfo: info, address: address, sessionTTL: ttl, now: now, publish: publish, queries: map[string]*jsonschema.Schema{}, requests: make(chan struct{}, 4), sessions: map[string]webSession{}}
	for _, tool := range definitions {
		if tool.name == "plan" {
			continue
		}
		compiler := jsonschema.NewCompiler()
		data, err := json.Marshal(tool.schema)
		if err != nil {
			return nil, err
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		location := "file:///infrafile/web/" + tool.name
		if err := compiler.AddResource(location, document); err != nil {
			return nil, err
		}
		compiled, err := compiler.Compile(location)
		if err != nil {
			return nil, err
		}
		w.queries[tool.name] = compiled
	}
	if err := w.freshPair(now()); err != nil {
		return nil, fmt.Errorf("无法发布配对码")
	}
	return w, nil
}

func (w *workbench) freshPair(now time.Time) error {
	w.pair, w.pairCSRF = "", ""
	code := rand.Text()
	if err := w.publish(code); err != nil {
		return err
	}
	w.pair, w.pairCSRF, w.pairExpires = code, rand.Text(), now.Add(10*time.Minute)
	return nil
}

func secureEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func singleCookie(r *http.Request, name string) string {
	var values []string
	for _, c := range r.Cookies() {
		if c.Name == name {
			values = append(values, c.Value)
		}
	}
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func (w *workbench) cookie(out http.ResponseWriter, name, value string, seconds int) {
	http.SetCookie(out, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: w.address.proxy.IsValid(), SameSite: http.SameSiteStrictMode, MaxAge: seconds})
}

func (w *workbench) currentSession(r *http.Request) (webSession, bool) {
	s, ok := w.sessions[singleCookie(r, sessionCookie)]
	return s, ok && w.now().Before(s.expires)
}

func (w *workbench) trustedRequest(r *http.Request) bool {
	if r.Host != strings.SplitN(w.address.origin, "://", 2)[1] || r.URL.IsAbs() || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] != w.address.origin) || (r.Method != http.MethodGet && len(origins) != 1) {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	peer, ipErr := netip.ParseAddr(host)
	if err != nil || ipErr != nil {
		return false
	}
	remote := w.address.proxy.IsValid()
	if remote {
		if peer.Unmap() != w.address.proxy {
			return false
		}
	} else if !peer.IsLoopback() {
		return false
	}
	for key, values := range r.Header {
		lower := strings.ToLower(key)
		if lower != "forwarded" && lower != "x-real-ip" && !strings.HasPrefix(lower, "x-forwarded-") {
			continue
		}
		if !remote || len(values) != 1 {
			return false
		}
		if lower == "x-forwarded-proto" && values[0] == "https" {
			continue
		}
		if lower == "x-forwarded-host" && values[0] == r.Host {
			continue
		}
		return false
	}
	return !remote || (len(r.Header.Values("X-Forwarded-Proto")) == 1 && r.Header.Get("X-Forwarded-Proto") == "https")
}

func webError(out http.ResponseWriter, status int) {
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	out.WriteHeader(status)
	json.NewEncoder(out).Encode(map[string]any{"version": jsonVersion, "ok": false, "error": http.StatusText(status)})
}

func (w *workbench) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	out.Header().Set("Cache-Control", "no-store")
	// no-referrer makes browser form POSTs send Origin: null, even on the same site.
	out.Header().Set("Referrer-Policy", "same-origin")
	out.Header().Set("X-Content-Type-Options", "nosniff")
	out.Header().Set("X-Frame-Options", "DENY")
	out.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if !w.trustedRequest(r) {
		webError(out, 403)
		return
	}
	select {
	case w.requests <- struct{}{}:
		defer func() { <-w.requests }()
	default:
		webError(out, 429)
		return
	}
	current, err := os.Lstat(w.dir)
	if err != nil || !current.IsDir() || !os.SameFile(w.rootInfo, current) {
		webError(out, 503)
		return
	}
	r.Body = http.MaxBytesReader(out, r.Body, 64<<10)
	if r.Method == http.MethodGet && r.URL.Path == "/app.css" {
		data, _ := workbenchFiles.ReadFile("web/app.css")
		out.Header().Set("Content-Type", "text/css; charset=utf-8")
		out.Write(data)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		w.page(out, r)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/pair" {
		w.login(out, r)
		return
	}
	w.mu.Lock()
	session, loggedIn := w.currentSession(r)
	csrf, expires := session.csrf, session.expires
	w.mu.Unlock()
	if !loggedIn {
		webError(out, 401)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/session" {
		out.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(out).Encode(map[string]any{"version": jsonVersion, "csrf": csrf, "expires_at": expires.UTC().Format(time.RFC3339)})
		return
	}
	if r.Method != http.MethodPost {
		webError(out, 404)
		return
	}
	if r.URL.Path == "/logout" || r.URL.Path == "/revoke-all" {
		w.logout(out, r)
		return
	}
	if len(r.Header.Values("X-CSRF-Token")) != 1 || !secureEqual(csrf, r.Header.Get("X-CSRF-Token")) {
		webError(out, 403)
		return
	}
	tool := strings.TrimPrefix(r.URL.Path, "/api/")
	schema, ok := w.queries[tool]
	if !strings.HasPrefix(r.URL.Path, "/api/") || !ok {
		webError(out, 404)
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
	if ctx.Err() != nil {
		webError(out, 504)
		return
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil || schema.Validate(value) != nil {
		webError(out, 400)
		return
	}
	_, problems, err := ledger.ParseDocument(ctx, "arguments", raw)
	if ctx.Err() != nil {
		webError(out, 504)
		return
	}
	if err != nil || len(problems) != 0 {
		webError(out, 400)
		return
	}
	var args mcpArgs
	if json.Unmarshal(raw, &args) != nil {
		webError(out, 400)
		return
	}
	w.mu.Lock()
	currentSession, stillLoggedIn := w.currentSession(r)
	stillLoggedIn = stillLoggedIn && secureEqual(currentSession.csrf, csrf)
	w.mu.Unlock()
	if !stillLoggedIn {
		webError(out, 401)
		return
	}
	result, err := mcpCall(ctx, w.dir, tool, args, w.now())
	if ctx.Err() != nil {
		webError(out, 504)
		return
	}
	if err != nil {
		webError(out, 422)
		return
	}
	if tool != "schema" {
		result = withoutSecrets(result)
		if !validate.SnapshotSafe(result) {
			webError(out, 422)
			return
		}
	}
	data, err := json.Marshal(result)
	if ctx.Err() != nil {
		webError(out, 504)
		return
	}
	if err != nil || len(data) > 4<<20 {
		webError(out, 422)
		return
	}
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	out.Write(data)
}

func (w *workbench) page(out http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	session, loggedIn := w.currentSession(r)
	if !loggedIn && (w.pair == "" || !now.Before(w.pairExpires)) {
		if w.freshPair(now) != nil {
			webError(out, 503)
			return
		}
	}
	csrf := session.csrf
	if !loggedIn {
		csrf = w.pairCSRF
		w.cookie(out, pairCookie, csrf, 600)
	}
	var page bytes.Buffer
	err := workbenchTemplate.ExecuteTemplate(&page, "serve.html", struct {
		LoggedIn            bool
		CSRF, Repo, Expires string
	}{loggedIn, csrf, filepath.Base(w.dir), session.expires.UTC().Format(time.RFC3339)})
	if err != nil {
		webError(out, 500)
		return
	}
	out.Header().Set("Content-Type", "text/html; charset=utf-8")
	out.Write(page.Bytes())
}

func formValues(r *http.Request, fields ...string) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || r.ParseForm() != nil || len(r.PostForm) != len(fields) {
		return false
	}
	for _, field := range fields {
		if len(r.PostForm[field]) != 1 {
			return false
		}
	}
	return true
}

func (w *workbench) login(out http.ResponseWriter, r *http.Request) {
	if !formValues(r, "csrf", "code") {
		webError(out, 400)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if !secureEqual(w.pairCSRF, singleCookie(r, pairCookie)) || !secureEqual(w.pairCSRF, r.PostForm.Get("csrf")) {
		webError(out, 403)
		return
	}
	if !now.Before(w.attemptReset) {
		w.attemptReset = now.Add(time.Minute)
		w.attempts = 0
	}
	if w.attempts >= 5 {
		webError(out, 429)
		return
	}
	w.attempts++
	if !now.Before(w.pairExpires) || !secureEqual(w.pair, r.PostForm.Get("code")) {
		webError(out, 403)
		return
	}
	for token, s := range w.sessions {
		if !now.Before(s.expires) {
			delete(w.sessions, token)
		}
	}
	if len(w.sessions) >= 8 {
		webError(out, 429)
		return
	}
	if w.freshPair(now) != nil {
		webError(out, 503)
		return
	}
	token := rand.Text()
	w.sessions[token] = webSession{csrf: rand.Text(), expires: now.Add(w.sessionTTL)}
	w.cookie(out, sessionCookie, token, int(w.sessionTTL.Seconds())+1)
	w.cookie(out, pairCookie, "", -1)
	http.Redirect(out, r, "/", http.StatusSeeOther)
}

func (w *workbench) logout(out http.ResponseWriter, r *http.Request) {
	if !formValues(r, "csrf") {
		webError(out, 400)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	session, ok := w.currentSession(r)
	if !ok || !secureEqual(session.csrf, r.PostForm.Get("csrf")) {
		webError(out, 403)
		return
	}
	delete(w.sessions, singleCookie(r, sessionCookie))
	if r.URL.Path == "/revoke-all" {
		clear(w.sessions)
	}
	w.cookie(out, sessionCookie, "", -1)
	if w.freshPair(w.now()) != nil {
		webError(out, 503)
		return
	}
	http.Redirect(out, r, "/", http.StatusSeeOther)
}
