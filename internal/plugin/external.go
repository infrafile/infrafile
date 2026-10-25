package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"time"

	"github.com/infrafile/infrafile/protocol"
)

type external struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Scanner
	stop   func() bool
	id     int
}

func openExternal(ctx context.Context, name string) (Client, error) {
	path, err := exec.LookPath("infrafile-plugin-" + name)
	if err != nil {
		return nil, failure("not-found", "PATH 上未找到插件可执行文件")
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.WaitDelay = 100 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, failure("start", "无法创建插件输入管道")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, failure("start", "无法创建插件输出管道")
	}
	// 不转发第三方日志，避免 stderr 或 RPC 错误中夹带凭据。
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, failure("start", "无法启动插件")
	}
	p := &external{cmd: cmd, stdin: stdin, stdout: stdout, reader: bufio.NewScanner(stdout)}
	p.reader.Buffer(make([]byte, 4096), 4<<20)
	// 后代进程可能仍持有管道；取消时关闭宿主端，保证读写也能结束。
	p.stop = context.AfterFunc(ctx, func() { stdin.Close(); stdout.Close() })
	return p, nil
}

func (p *external) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.id++
	request, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", p.id, method, params})
	if err != nil || protocol.Validate("request", request) != nil {
		return nil, failure("protocol", "插件请求不符合协议")
	}
	_, err = p.stdin.Write(append(request, '\n'))
	if err != nil || !p.reader.Scan() {
		if ctx.Err() != nil {
			return nil, failure("timeout", "插件超时或被取消")
		}
		return nil, failure("protocol", "插件未返回完整响应或响应超过 4 MiB")
	}
	response := p.reader.Bytes()
	if protocol.Validate("response", response) != nil {
		return nil, failure("protocol", "插件 JSON-RPC 响应不合法")
	}
	var reply struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(response, &reply) != nil || reply.ID != p.id {
		return nil, failure("protocol", "插件响应 ID 与请求不一致")
	}
	if reply.Error != nil {
		return nil, failure("plugin", "插件返回执行错误")
	}
	return reply.Result, nil
}

func (p *external) Close() {
	p.stop()
	p.stdin.Close()
	p.stdout.Close()
	// 协议会话到此结束，插件不必实现额外的 shutdown 方法。
	p.cmd.Process.Kill()
	p.cmd.Wait()
}
