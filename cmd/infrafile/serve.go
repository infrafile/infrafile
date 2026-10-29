package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type serveOptions struct {
	dir, listen, externalURL, trustedProxy, pairFile string
	sessionTTL                                       time.Duration
}

type serveAddress struct {
	origin string
	proxy  netip.Addr
}

func (o serveOptions) address() (serveAddress, error) {
	host, port, err := net.SplitHostPort(o.listen)
	ip, ipErr := netip.ParseAddr(host)
	if err != nil || ipErr != nil || port == "" || o.sessionTTL <= 0 || o.sessionTTL > 24*time.Hour {
		return serveAddress{}, fmt.Errorf("listen 必须是 IP:port，session-ttl 必须在 0 到 24h 之间")
	}
	if o.externalURL == "" && o.trustedProxy == "" && ip.IsLoopback() {
		return serveAddress{origin: "http://" + o.listen}, nil
	}
	u, err := url.Parse(o.externalURL)
	proxy, proxyErr := netip.ParseAddr(o.trustedProxy)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || proxyErr != nil || proxy.IsUnspecified() || proxy.IsMulticast() || strings.ContainsAny(u.Host, "\\ \t\r\n") {
		return serveAddress{}, fmt.Errorf("远程模式必须同时指定 HTTPS external-url 和单个可信代理 IP")
	}
	return serveAddress{origin: "https://" + u.Host, proxy: proxy.Unmap()}, nil
}

func newServeCmd(global *globalOpts) *cobra.Command {
	o := serveOptions{}
	cmd := &cobra.Command{Use: "serve", Short: "启动单仓单用户工作台（只读）", Args: maxArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if global.json {
			return fmt.Errorf("serve 不支持 --json；只读 API 返回 JSON")
		}
		address, err := o.address()
		if err != nil {
			return err
		}
		o.dir, err = filepath.Abs(o.dir)
		if err == nil {
			o.dir, err = filepath.EvalSymlinks(o.dir)
		}
		if err != nil {
			return fmt.Errorf("无法固定数据仓目录")
		}
		if o.pairFile != "" && !outsideRepo(o.dir, o.pairFile) {
			return fmt.Errorf("配对文件必须位于数据仓外")
		}
		publish, cleanup, err := pairingOutput(o.pairFile, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer cleanup()
		app, err := newWorkbench(cmd.Context(), o.dir, address, o.sessionTTL, time.Now, publish)
		if err != nil {
			return err
		}
		listener, err := net.Listen("tcp", o.listen)
		if err != nil {
			return fmt.Errorf("工作台监听失败")
		}
		defer listener.Close()
		if !address.proxy.IsValid() {
			app.address.origin = "http://" + listener.Addr().String()
		}
		server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return cmd.Context() }}
		stopped := make(chan struct{})
		shutdownDone := make(chan struct{})
		defer func() { close(stopped); <-shutdownDone }()
		go func() {
			defer close(shutdownDone)
			select {
			case <-cmd.Context().Done():
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if server.Shutdown(ctx) != nil {
					server.Close()
				}
			case <-stopped:
			}
		}()
		fmt.Fprintf(cmd.ErrOrStderr(), "工作台：%s\n", app.address.origin)
		err = server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("工作台服务停止")
	}}
	cmd.Flags().StringVar(&o.dir, "dir", ".", "固定的数据仓目录")
	cmd.Flags().StringVar(&o.listen, "listen", "127.0.0.1:8080", "监听 IP:port；默认仅回环")
	cmd.Flags().StringVar(&o.externalURL, "external-url", "", "远程模式的唯一 HTTPS 外部地址")
	cmd.Flags().StringVar(&o.trustedProxy, "trusted-proxy", "", "远程模式的单个可信代理 IP")
	cmd.Flags().StringVar(&o.pairFile, "pair-file", "", "后台启动时将配对码写入仓外新建的 0600 文件；退出时删除")
	cmd.Flags().DurationVar(&o.sessionTTL, "session-ttl", 8*time.Hour, "会话固定有效期，最长 24h")
	return cmd
}

// Pairing material goes only to a terminal or an explicitly requested private file.
func pairingOutput(path string, out io.Writer) (func(string) error, func(), error) {
	if path == "" {
		f, ok := out.(*os.File)
		if !ok || !term.IsTerminal(int(f.Fd())) {
			return nil, nil, fmt.Errorf("后台启动须指定 --pair-file，配对码不能写普通日志")
		}
		return func(code string) error {
			if code == "" {
				return nil
			}
			_, err := fmt.Fprintf(out, "配对码（10 分钟内一次有效）：%s\n", code)
			return err
		}, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("配对文件必须是可新建的私有文件")
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("无法核对配对文件")
	}
	check := func() bool {
		current, err := os.Lstat(path)
		return err == nil && current.Mode().IsRegular() && current.Mode().Perm() == 0600 && os.SameFile(info, current)
	}
	cleanup := func() {
		if check() {
			os.Remove(path)
		}
		f.Close()
	}
	return func(code string) error {
		if !check() {
			return fmt.Errorf("配对文件已变化")
		}
		if err := f.Truncate(0); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if code != "" {
			if _, err := io.WriteString(f, code+"\n"); err != nil {
				return err
			}
		}
		return f.Sync()
	}, cleanup, nil
}

func outsideRepo(dir, file string) bool {
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, filepath.Join(parent, filepath.Base(absolute)))
	return err == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
