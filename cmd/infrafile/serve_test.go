package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type webFixture struct {
	app                 *workbench
	clock               time.Time
	code, session, csrf string
}

func newWebFixture(t *testing.T, dir string) *webFixture {
	t.Helper()
	f := &webFixture{clock: time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)}
	var err error
	f.app, err = newWorkbench(context.Background(), dir, serveAddress{origin: "http://127.0.0.1:8080"}, time.Hour, func() time.Time { return f.clock }, func(code string) error { f.code = code; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *webFixture) request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, f.app.address.origin+path, strings.NewReader(body))
	r.URL.Scheme, r.URL.Host = "", ""
	r.RemoteAddr = "127.0.0.1:12345"
	if method == http.MethodPost {
		r.Header.Set("Origin", f.app.address.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", f.csrf)
	}
	if f.session != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session})
	}
	return r
}

func (f *webFixture) send(r *http.Request) *httptest.ResponseRecorder {
	out := httptest.NewRecorder()
	f.app.ServeHTTP(out, r)
	return out
}

func (f *webFixture) login(t *testing.T) {
	t.Helper()
	page := f.send(f.request("GET", "/", ""))
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == pairCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || !strings.Contains(page.Body.String(), cookie.Value) {
		t.Fatal("missing pairing CSRF cookie/form")
	}
	form := url.Values{"csrf": {cookie.Value}, "code": {f.code}}
	oldCode := f.code
	r := f.request("POST", "/pair", form.Encode())
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	out := f.send(r)
	if out.Code != 303 {
		t.Fatal(out.Code, out.Body.String())
	}
	for _, c := range out.Result().Cookies() {
		if c.Name == sessionCookie {
			f.session = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure != f.app.address.proxy.IsValid() {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	if f.session == "" || f.app.pair == oldCode {
		t.Fatal("pair not consumed")
	}
	var value struct {
		CSRF string `json:"csrf"`
	}
	state := f.send(f.request("GET", "/api/session", ""))
	if state.Code != 200 || json.Unmarshal(state.Body.Bytes(), &value) != nil || value.CSRF == "" {
		t.Fatal(state.Body.String())
	}
	f.csrf = value.CSRF
}

func TestWorkbenchLifecycle(t *testing.T) {
	f := newWebFixture(t, homelab)
	if out := f.send(f.request("POST", "/api/list", "{}")); out.Code != 401 {
		t.Fatal(out.Code)
	}
	oldCode := f.code
	f.login(t)
	if out := f.send(f.request("POST", "/api/list", "{}")); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	form := url.Values{"csrf": {f.csrf}}
	r := f.request("POST", "/logout", form.Encode())
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if out := f.send(r); out.Code != 303 {
		t.Fatal(out.Code)
	}
	if f.code == "" || f.code == oldCode {
		t.Fatal("logout did not rotate pairing")
	}
	if out := f.send(f.request("GET", "/api/session", "")); out.Code != 401 {
		t.Fatal("revoked session accepted")
	}
	f.session = ""
	f.login(t)
	f.clock = f.clock.Add(time.Hour)
	if out := f.send(f.request("GET", "/api/session", "")); out.Code != 401 {
		t.Fatal("expired session accepted")
	}
	f.session = ""
	f.login(t)
	restarted := newWebFixture(t, homelab)
	restarted.session = f.session
	if out := restarted.send(restarted.request("GET", "/api/session", "")); out.Code != 401 {
		t.Fatal("restart retained session")
	}
}

func TestWorkbenchPairingRefusals(t *testing.T) {
	for _, mode := range []string{"csrf", "cookie", "origin", "expired", "replay", "attempts", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			f := newWebFixture(t, homelab)
			code, csrf := f.code, f.app.pairCSRF
			form := url.Values{"csrf": {csrf}, "code": {code}}
			r := f.request("POST", "/pair", form.Encode())
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(&http.Cookie{Name: pairCookie, Value: csrf})
			want := 403
			switch mode {
			case "csrf":
				r.Body = io.NopCloser(strings.NewReader("csrf=wrong&code=" + code))
			case "cookie":
				r.Header.Del("Cookie")
			case "origin":
				r.Header.Del("Origin")
			case "expired":
				f.clock = f.clock.Add(10 * time.Minute)
			case "replay":
				f.login(t)
			case "attempts":
				f.app.attempts = 5
				f.app.attemptReset = f.clock.Add(time.Minute)
				want = 429
			case "duplicate":
				r.Body = io.NopCloser(strings.NewReader(form.Encode() + "&code=other"))
				want = 400
			}
			out := f.send(r)
			if out.Code != want || strings.Contains(out.Body.String(), code) {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
}

func TestWorkbenchDevicesAndRevokeAll(t *testing.T) {
	f := newWebFixture(t, homelab)
	f.login(t)
	second := *f
	second.session = ""
	second.login(t)
	if second.session == f.session {
		t.Fatal("devices share session token")
	}
	for _, device := range []*webFixture{f, &second} {
		if out := device.send(device.request("GET", "/api/session", "")); out.Code != 200 {
			t.Fatal("other device login revoked session")
		}
	}
	r := f.request("POST", "/revoke-all", url.Values{"csrf": {f.csrf}}.Encode())
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if out := f.send(r); out.Code != 303 {
		t.Fatal(out.Code)
	}
	for _, device := range []*webFixture{f, &second} {
		if out := device.send(device.request("GET", "/api/session", "")); out.Code != 401 {
			t.Fatal("revoke-all retained a device")
		}
	}
}

func TestWorkbenchSessionLimitAndPublicationFailure(t *testing.T) {
	f := newWebFixture(t, homelab)
	for i := 0; i < 8; i++ {
		f.app.sessions[strings.Repeat("x", i+1)] = webSession{expires: f.clock.Add(time.Hour)}
	}
	form := url.Values{"csrf": {f.app.pairCSRF}, "code": {f.code}}
	r := f.request("POST", "/pair", form.Encode())
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: pairCookie, Value: f.app.pairCSRF})
	if out := f.send(r); out.Code != 429 || len(f.app.sessions) != 8 {
		t.Fatal("unbounded sessions")
	}
	f.clock = f.clock.Add(time.Hour)
	f.login(t)
	if len(f.app.sessions) != 1 {
		t.Fatal("expired sessions not reclaimed")
	}
	f.app.publish = func(string) error { return io.ErrClosedPipe }
	r = f.request("POST", "/logout", url.Values{"csrf": {f.csrf}}.Encode())
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if out := f.send(r); out.Code != 503 {
		t.Fatal("pair publication failure ignored")
	}
	if out := f.send(f.request("GET", "/api/session", "")); out.Code != 401 {
		t.Fatal("publication failure retained revoked session")
	}
}

func TestWorkbenchRequestBoundary(t *testing.T) {
	f := newWebFixture(t, homelab)
	f.login(t)
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"host", func(r *http.Request) { r.Host = "attacker.invalid" }, 403},
		{"cross-origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }, 403},
		{"null-origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"duplicate-origin", func(r *http.Request) { r.Header.Add("Origin", f.app.address.origin) }, 403},
		{"missing-origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"same-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
		{"forwarded", func(r *http.Request) { r.Header.Set("Forwarded", "proto=https") }, 403},
		{"proxy-proto", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, 403},
		{"remote-peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" }, 403},
		{"csrf", func(r *http.Request) { r.Header.Set("X-CSRF-Token", "wrong") }, 403},
		{"duplicate-session", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session}) }, 401},
		{"query-token", func(r *http.Request) { r.URL.RawQuery = "token=bad" }, 403},
		{"encoded-path", func(r *http.Request) { r.URL.RawPath = "/api/%6cist" }, 403},
		{"absolute-uri", func(r *http.Request) { r.URL.Scheme = "http"; r.URL.Host = r.Host }, 403},
		{"body-limit", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", 65537))) }, 413},
		{"media-type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"cancelled", func(r *http.Request) {
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			*r = *r.WithContext(ctx)
		}, 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.request("POST", "/api/list", "{}")
			tc.change(r)
			out := f.send(r)
			if out.Code != tc.want {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
	for _, path := range []string{"/api/plan", "/api/apply", "/api/sync", "/api/import", "/api/secret", "/api/run", "/api/../infrafile.yaml", "/infrafile.yaml", "/web/serve.html"} {
		if out := f.send(f.request("POST", path, "{}")); out.Code != 404 {
			t.Fatal(path, out.Code)
		}
	}
	for _, body := range []string{`{"dir":"/tmp"}`, `{"key":"../../outside"}`, `{"command":"sync"}`, `{"labels":{}}`, `{"key":"Server/web1","key":"Server/web1"}`, `{} {}`, `null`} {
		out := f.send(f.request("POST", "/api/list", body))
		if out.Code != 400 {
			t.Fatal(body, out.Code, out.Body.String())
		}
	}
	for i := 0; i < cap(f.app.requests); i++ {
		f.app.requests <- struct{}{}
	}
	if out := f.send(f.request("GET", "/", "")); out.Code != 429 {
		t.Fatal("unbounded concurrent request")
	}
	for i := 0; i < cap(f.app.requests); i++ {
		<-f.app.requests
	}
}

func TestWorkbenchQueriesShareCLIAndMCP(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	t.Chdir(dir)
	before := mcpFiles(t)
	oldHTTP, oldDNS := http.DefaultTransport, net.DefaultResolver
	http.DefaultTransport = cloudReplay(func(*http.Request) (*http.Response, error) { t.Fatal("Web query contacted HTTP"); return nil, nil })
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("Web query contacted DNS")
		return nil, nil
	}}
	t.Cleanup(func() { http.DefaultTransport = oldHTTP; net.DefaultResolver = oldDNS })
	for _, tool := range []string{"list", "show", "expiry", "audit", "inspect", "validate", "schema", "capabilities"} {
		t.Run(tool, func(t *testing.T) {
			args := mcpArgs{}
			body := "{}"
			if tool == "show" {
				args.Key = "Server/web1"
				body = `{"key":"Server/web1"}`
			}
			expected, err := mcpCall(context.Background(), dir, tool, args, f.clock)
			if err != nil {
				t.Fatal(err)
			}
			if tool != "schema" {
				expected = withoutSecrets(expected)
			}
			data, _ := json.Marshal(expected)
			out := f.send(f.request("POST", "/api/"+tool, body))
			if out.Code != 200 || !bytes.Equal(data, out.Body.Bytes()) {
				t.Fatal(tool, out.Code, out.Body.String())
			}
			if !strings.Contains(out.Header().Get("Content-Security-Policy"), "default-src 'none'") || out.Header().Get("Cache-Control") != "no-store" || out.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing browser protections")
			}
		})
	}
	if !reflect.DeepEqual(before, mcpFiles(t)) {
		t.Fatal("queries changed ledger")
	}
}

func TestWorkbenchHTMLAndSymlinkBoundary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "<img src=x onerror=alert(1)>")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	page := f.send(f.request("GET", "/", ""))
	if strings.Contains(page.Body.String(), "<img") || !strings.Contains(page.Body.String(), "&lt;img") {
		t.Fatal("HTML injection rendered")
	}
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("synthetic-outside-private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "inventory", "servers", "outside.yaml")); err != nil {
		t.Skip("symlinks unavailable")
	}
	out := f.send(f.request("POST", "/api/list", "{}"))
	if out.Code != 422 || strings.Contains(out.Body.String(), "synthetic-outside") {
		t.Fatal(out.Code, out.Body.String())
	}
	if err := os.Rename(dir, dir+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+"-original", dir); err != nil {
		t.Fatal(err)
	}
	if out := f.send(f.request("GET", "/", "")); out.Code != 503 {
		t.Fatal("replaced root accepted")
	}
}

func TestWorkbenchSensitiveSnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	writeImportFile(t, filepath.Join(dir, "state/cloudflare/home.yaml"), "apiVersion: infrafile/v1alpha1\nkind: Snapshot\nmetadata: {name: home}\nspec: {password: synthetic-web-private-value}\n")
	out := f.send(f.request("POST", "/api/audit", "{}"))
	if out.Code != 422 || strings.Contains(out.Body.String(), "synthetic-web-private-value") {
		t.Fatal(out.Code, out.Body.String())
	}
}

type webReadHook struct {
	io.Reader
	run func()
}

func (r *webReadHook) Read(p []byte) (int, error) {
	if r.run != nil {
		run := r.run
		r.run = nil
		run()
	}
	return r.Reader.Read(p)
}

func TestWorkbenchRevocationWhileReadingRequest(t *testing.T) {
	f := newWebFixture(t, homelab)
	f.login(t)
	r := f.request("POST", "/api/list", "{}")
	r.Body = io.NopCloser(&webReadHook{Reader: strings.NewReader("{}"), run: func() { f.app.mu.Lock(); clear(f.app.sessions); f.app.mu.Unlock() }})
	if out := f.send(r); out.Code != 401 {
		t.Fatal("revocation during body read ignored", out.Code)
	}
}

func TestWorkbenchResultLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(homelab)); err != nil {
		t.Fatal(err)
	}
	f := newWebFixture(t, dir)
	f.login(t)
	path := filepath.Join(dir, "inventory/servers/web1.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("跑博客和 wiki 的前端机"), bytes.Repeat([]byte("z"), 4<<20), 1)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := mcpCall(context.Background(), dir, "show", mcpArgs{Key: "Server/web1"}, f.clock)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) <= 4<<20 {
		t.Fatal("fixture did not reach result size boundary")
	}
	out := f.send(f.request("POST", "/api/show", `{"key":"Server/web1"}`))
	if out.Code != 422 || out.Body.Len() > 1024 {
		t.Fatal("unbounded query result", out.Code, out.Body.Len())
	}
}

func TestServeModesAndPairFile(t *testing.T) {
	for _, tc := range []struct {
		listen, external, proxy string
		ttl                     time.Duration
		ok                      bool
	}{
		{"127.0.0.1:0", "", "", time.Hour, true}, {"[::1]:8080", "", "", time.Hour, true},
		{"0.0.0.0:8080", "", "", time.Hour, false}, {"localhost:8080", "", "", time.Hour, false},
		{"0.0.0.0:8080", "https://workbench.invalid", "127.0.0.1", time.Hour, true},
		{"127.0.0.1:8080", "http://workbench.invalid", "127.0.0.1", time.Hour, false},
		{"127.0.0.1:8080", "https://workbench.invalid/path", "127.0.0.1", time.Hour, false},
		{"127.0.0.1:8080", "https://user@workbench.invalid", "127.0.0.1", time.Hour, false},
		{"127.0.0.1:8080", "https://workbench.invalid?token=x", "127.0.0.1", time.Hour, false},
		{"127.0.0.1:8080", "https://workbench.invalid", "0.0.0.0", time.Hour, false},
		{"127.0.0.1:8080", "https://workbench.invalid", "127.0.0.1/32", time.Hour, false},
		{"127.0.0.1:8080", "", "", 0, false}, {"127.0.0.1:8080", "", "", 25 * time.Hour, false},
	} {
		_, err := (serveOptions{listen: tc.listen, externalURL: tc.external, trustedProxy: tc.proxy, sessionTTL: tc.ttl}).address()
		if (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	var log bytes.Buffer
	if _, _, err := pairingOutput("", &log); err == nil || log.Len() != 0 {
		t.Fatal("pairing leaked to ordinary log")
	}
	path := filepath.Join(t.TempDir(), "pair")
	publish, cleanup, err := pairingOutput(path, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := publish("synthetic-pair-code"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if _, _, err := pairingOutput(path, &log); err == nil {
		t.Fatal("existing file overwritten")
	}
	if err := publish(""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); len(data) != 0 {
		t.Fatal("consumed code retained")
	}
	if err := os.Rename(path, path+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+"-saved", path); err == nil {
		if publish("new-code") == nil {
			t.Fatal("changed pair file accepted")
		}
	}
	repo := t.TempDir()
	if outsideRepo(repo, filepath.Join(repo, "pair")) || !outsideRepo(repo, filepath.Join(t.TempDir(), "pair")) {
		t.Fatal("pair file repository boundary")
	}
}

func TestWorkbenchRemoteBoundaryAndHTTPSProxy(t *testing.T) {
	f := newWebFixture(t, homelab)
	f.app.address = serveAddress{origin: "https://workbench.invalid", proxy: netip.MustParseAddr("127.0.0.1")}
	for _, mode := range []string{"accepted", "direct", "http", "host", "forwarded", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			r := f.request("GET", "/", "")
			r.Header.Set("X-Forwarded-Proto", "https")
			switch mode {
			case "direct":
				r.RemoteAddr = "192.0.2.1:1234"
			case "http":
				r.Header.Set("X-Forwarded-Proto", "http")
			case "host":
				r.Header.Set("X-Forwarded-Host", "evil.invalid")
			case "forwarded":
				r.Header.Set("X-Forwarded-For", "127.0.0.1")
			case "duplicate":
				r.Header.Add("X-Forwarded-Proto", "https")
			}
			out := f.send(r)
			want := 403
			if mode == "accepted" {
				want = 200
			}
			if out.Code != want {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
	backend := httptest.NewServer(f.app)
	defer backend.Close()
	backendURL, _ := url.Parse(backend.URL)
	var external string
	proxy := httptest.NewTLSServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(backendURL)
		u, _ := url.Parse(external)
		r.Out.Host = u.Host
		r.Out.Header.Set("X-Forwarded-Proto", "https")
	}})
	defer proxy.Close()
	external = proxy.URL
	f.app.address.origin = external
	client := proxy.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(external + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	u, _ := url.Parse(external)
	csrf := ""
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == pairCookie {
			csrf = c.Value
		}
	}
	request, _ := http.NewRequest("POST", external+"/pair", strings.NewReader(url.Values{"csrf": {csrf}, "code": {f.code}}.Encode()))
	request.Header.Set("Origin", external)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatal(response.StatusCode)
	}
	secure := false
	for _, c := range response.Cookies() {
		if c.Name == sessionCookie {
			secure = c.Secure
		}
	}
	if !secure {
		t.Fatal("HTTPS proxy issued insecure cookie")
	}
	response, err = client.Get(external + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
}

func TestWorkbenchConcurrentRequests(t *testing.T) {
	f := newWebFixture(t, homelab)
	f.login(t)
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Go(func() {
			out := f.send(f.request("POST", "/api/capabilities", "{}"))
			if out.Code != 200 && out.Code != 429 {
				t.Errorf("unexpected %d", out.Code)
			}
		})
	}
	group.Wait()
}
