// Package mcpclient 的 stdio 传输：把 MCP server 作为子进程拉起来，
// 一行一个 JSON-RPC 帧走 stdin/stdout。
package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResponse
	pendMu  sync.Mutex
	done    chan struct{}
	stderr  strings.Builder
	stderrM sync.Mutex
}

func startStdio(config Config) (*stdioTransport, error) {
	if strings.TrimSpace(config.Command) == "" {
		return nil, errors.New("MCP server 命令为空")
	}
	cmd := exec.Command(config.Command, config.Args...)
	cmd.Env = os.Environ()
	for key, value := range config.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 MCP server 失败：%w", err)
	}

	t := &stdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		pending: map[int64]chan rpcResponse{},
		done:    make(chan struct{}),
	}
	go t.readLoop(stdout)
	go t.drainStderr(stderr)
	return t, nil
}

func (t *stdioTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.pendMu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan rpcResponse, 1)
	t.pending[id] = ch
	t.pendMu.Unlock()

	if err := t.write(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		t.removePending(id)
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s（code %d）", resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-ctx.Done():
		t.removePending(id)
		return nil, ctx.Err()
	case <-t.done:
		return nil, errors.New("MCP server 已关闭")
	}
}

func (t *stdioTransport) Notify(method string, params any) {
	_ = t.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *stdioTransport) Close() {
	select {
	case <-t.done:
		return
	default:
	}
	close(t.done)
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		// 先礼后兵：给 3 秒自己退，不退再杀。
		finished := make(chan struct{})
		go func() {
			_ = t.cmd.Wait()
			close(finished)
		}()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			_ = t.cmd.Process.Kill()
			<-finished
		}
	}
	t.failAll(errors.New("MCP server 已关闭"))
}

// Diagnostics 返回子进程 stderr 的尾巴。
//
// MCP server 起不来时，真正的原因几乎总是印在 stderr 上（找不到命令、
// 缺依赖、配置错）。不带上它，用户看到的只有一句「握手失败」。
func (t *stdioTransport) Diagnostics() string {
	t.stderrM.Lock()
	defer t.stderrM.Unlock()
	text := strings.TrimSpace(t.stderr.String())
	if text == "" {
		return ""
	}
	return "\n" + text
}

func (t *stdioTransport) write(req rpcRequest) error {
	encoded, err := json.Marshal(req)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.stdin.Write(append(encoded, '\n'))
	return err
}

func (t *stdioTransport) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil || resp.ID == nil {
			// 通知或坏行：本客户端不处理服务端通知，直接跳过。
			continue
		}
		t.pendMu.Lock()
		ch, ok := t.pending[*resp.ID]
		if ok {
			delete(t.pending, *resp.ID)
		}
		t.pendMu.Unlock()
		if ok {
			ch <- resp
		}
	}
	t.failAll(errors.New("MCP server 输出已关闭"))
}

func (t *stdioTransport) drainStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		t.stderrM.Lock()
		// 只保留尾部，防止一个疯狂刷日志的 server 把内存吃掉。
		if t.stderr.Len() > 8192 {
			tail := t.stderr.String()
			t.stderr.Reset()
			t.stderr.WriteString(tail[len(tail)-4096:])
		}
		t.stderr.WriteString(scanner.Text())
		t.stderr.WriteString("\n")
		t.stderrM.Unlock()
	}
}

func (t *stdioTransport) removePending(id int64) {
	t.pendMu.Lock()
	delete(t.pending, id)
	t.pendMu.Unlock()
}

func (t *stdioTransport) failAll(err error) {
	t.pendMu.Lock()
	defer t.pendMu.Unlock()
	for id, ch := range t.pending {
		ch <- rpcResponse{Error: &rpcError{Code: -32000, Message: err.Error()}}
		delete(t.pending, id)
	}
}
