package plugin_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/infrafile/infrafile/internal/plugin"
	"github.com/infrafile/infrafile/protocol"
)

func TestMain(m *testing.M) {
	if os.Getenv("INFRAFILE_SSH_HELPER") == "1" && filepath.Base(os.Args[0]) == "ssh" {
		args := strings.Join(os.Args[1:], " ")
		for _, required := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", "UpdateHostKeys=no", "PermitLocalCommand=no", "uname -srm"} {
			if !strings.Contains(args, required) {
				os.Exit(9)
			}
		}
		if os.Getenv("INFRAFILE_SSH_MODE") == "hang" {
			time.Sleep(time.Minute)
		}
		if os.Getenv("INFRAFILE_SSH_MODE") == "error" {
			fmt.Fprintln(os.Stderr, "synthetic-private-error")
			os.Exit(1)
		}
		data, err := os.ReadFile(os.Getenv("INFRAFILE_SSH_FIXTURE"))
		if err != nil {
			os.Exit(8)
		}
		os.Stdout.Write(data)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSSHReadOnlyCommandAndTimeout(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "ssh")); err != nil {
		t.Skip("系统不支持测试命令链接")
	}
	fixture, err := filepath.Abs("testdata/ssh.txt")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INFRAFILE_SSH_HELPER", "1")
	t.Setenv("INFRAFILE_SSH_FIXTURE", fixture)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	data := syncBuiltin(t, "ssh", map[string]any{"host": "example.com", "user": "observer", "port": 2222})
	if !strings.Contains(string(data), `"architecture":"x86_64"`) || !strings.Contains(string(data), `"release":"6.1.0-example"`) {
		t.Fatalf("SSH result: %s", data)
	}
	for _, mode := range []string{"error", "hang"} {
		t.Setenv("INFRAFILE_SSH_MODE", mode)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		client, err := plugin.Open(ctx, "ssh", dir)
		if err != nil {
			t.Fatal(err)
		}
		_, err = plugin.Sync(ctx, client, protocol.SyncParams{Instance: "test", Config: map[string]any{"host": "example.com"}})
		client.Close()
		cancel()
		if err == nil || strings.Contains(err.Error(), "synthetic-private-error") {
			t.Fatalf("unsafe SSH error: %v", err)
		}
	}
}

func TestDNSRecordedSync(t *testing.T) {
	fixture, err := os.ReadFile("testdata/dns.json")
	if err != nil {
		t.Fatal(err)
	}
	var records struct {
		A     string `json:"a"`
		AAAA  string `json:"aaaa"`
		CNAME string `json:"cname"`
	}
	if json.Unmarshal(fixture, &records) != nil {
		t.Fatal("invalid fixture")
	}
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var size uint16
			if binary.Read(server, binary.BigEndian, &size) != nil {
				return
			}
			query := make([]byte, size)
			if _, err := io.ReadFull(server, query); err != nil {
				return
			}
			end := 12
			for end < len(query) && query[end] != 0 {
				end += int(query[end]) + 1
			}
			if end+5 > len(query) {
				return
			}
			typ := binary.BigEndian.Uint16(query[end+1 : end+3])
			var canonical []byte
			for _, label := range strings.Split(records.CNAME, ".") {
				canonical = append(canonical, byte(len(label)))
				canonical = append(canonical, label...)
			}
			canonical = append(canonical, 0)
			var value []byte
			switch typ {
			case 1:
				value = net.ParseIP(records.A).To4()
			case 28:
				value = net.ParseIP(records.AAAA).To16()
			case 5:
				for _, label := range strings.Split(records.CNAME, ".") {
					value = append(value, byte(len(label)))
					value = append(value, label...)
				}
				value = append(value, 0)
			}
			response := append([]byte(nil), query[:end+5]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[6:8], 1)
			binary.BigEndian.PutUint16(response[10:12], 0)
			if typ == 1 || typ == 28 {
				binary.BigEndian.PutUint16(response[6:8], 2)
				response = append(response, 0xc0, 0x0c, 0, 5, 0, 1, 0, 0, 1, 44, 0, byte(len(canonical)))
				response = append(response, canonical...)
				response = append(response, canonical...)
			} else {
				response = append(response, 0xc0, 0x0c)
			}
			response = append(response, byte(typ>>8), byte(typ), 0, 1, 0, 0, 1, 44, byte(len(value)>>8), byte(len(value)))
			response = append(response, value...)
			binary.Write(server, binary.BigEndian, uint16(len(response)))
			server.Write(response)
		}()
		return client, nil
	}}
	t.Cleanup(func() { net.DefaultResolver = old })
	data := syncBuiltin(t, "dns", map[string]any{"host": "example.com"})
	for _, want := range []string{"192.0.2.1", "2001:db8::1", "canonical.example.com"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}
}

func TestWHOISRecordedSyncAndCancellation(t *testing.T) {
	fixture, err := os.ReadFile("testdata/whois.txt")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				if line == "example.com\r\n" {
					conn.Write(fixture)
				} else {
					io.Copy(io.Discard, conn)
				}
			}()
		}
	}()
	data := syncBuiltin(t, "whois", map[string]any{"domain": "example.com", "server": listener.Addr().String()})
	if !strings.Contains(string(data), `"expires_at":"2030-01-01"`) || strings.Contains(string(data), "private-contact") || strings.Contains(string(data), "Last update") {
		t.Fatalf("WHOIS result: %s", data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client, err := plugin.Open(ctx, "whois", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline, _ := ctx.Deadline()
	_, err = plugin.Sync(ctx, client, protocol.SyncParams{Instance: "test", Config: map[string]any{"domain": "hang.example.com", "server": listener.Addr().String()}})
	// 套接字 deadline 可早于 context 定时器通知，按同一个截止时间验证退出。
	if err == nil || time.Now().Before(deadline) {
		t.Fatalf("WHOIS timeout: %v", err)
	}
}

func TestTLSObservesCertificateWithoutTrustingIt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("TLS probe must not send HTTP") }))
	defer server.Close()
	host, portString, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portString)
	data := syncBuiltin(t, "tls", map[string]any{"host": host, "port": port})
	var result struct {
		Verified    bool `json:"verified"`
		Certificate struct {
			SHA256   string `json:"sha256"`
			NotAfter string `json:"not_after"`
		} `json:"certificate"`
	}
	if json.Unmarshal(data, &result) != nil || result.Verified || result.Certificate.NotAfter == "" || result.Certificate.SHA256 != fmt.Sprintf("%x", sha256.Sum256(server.Certificate().Raw)) {
		t.Fatalf("TLS result: %s", data)
	}
}

func TestTLSKeepsExpiredCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	host, portString, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	data := syncBuiltin(t, "tls", map[string]any{"host": host, "port": port})
	if !strings.Contains(string(data), `"verification":"expired-or-not-yet-valid"`) || !strings.Contains(string(data), `"not_after":"2001-01-01T00:00:00Z"`) || !strings.Contains(string(data), `"verified":false`) {
		t.Fatalf("expired certificate disappeared: %s", data)
	}
}

func syncBuiltin(t *testing.T, name string, config map[string]any) json.RawMessage {
	t.Helper()
	client, err := plugin.Open(context.Background(), name, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	data, err := plugin.Sync(context.Background(), client, protocol.SyncParams{Instance: "test", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
