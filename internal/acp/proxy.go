package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

type Proxy struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.Reader
	sessionID  string
	sessionMux sync.RWMutex
	done       chan struct{}
	doneOnce   sync.Once
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type SessionNewResult struct {
	SessionID string `json:"sessionId"`
}

func NewProxy() *Proxy {
	return &Proxy{
		done: make(chan struct{}),
	}
}

func (p *Proxy) Start(ctx context.Context, agentPath string, agentArgs []string, cwd string) error {
	childCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	p.cmd = exec.CommandContext(childCtx, agentPath, agentArgs...)
	p.cmd.Dir = cwd

	p.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	var err error
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	p.stdout, err = p.cmd.StdoutPipe()
	if err != nil {
		cancel()
		p.stdin.Close()
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	p.cmd.Stderr = os.Stderr

	if err := p.cmd.Start(); err != nil {
		cancel()
		p.stdin.Close()
		return fmt.Errorf("starting agent: %w", err)
	}

	return nil
}

func (p *Proxy) Forward() error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	p.wg.Add(2)
	go p.forwardToAgent()
	go p.forwardFromAgent()

	select {
	case <-sigChan:
		p.Shutdown()
	case <-p.done:
	case <-p.agentDone():
	}

	p.wg.Wait()
	return p.cmd.Wait()
}

func (p *Proxy) forwardToAgent() {
	defer p.wg.Done()
	defer p.stdin.Close()

	reader := bufio.NewReader(os.Stdin)
	encoder := json.NewEncoder(p.stdin)

	for {
		select {
		case <-p.done:
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				select {
				case <-p.done:
				default:
					p.markDone()
				}
			}
			return
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if err := encoder.Encode(&msg); err != nil {
			p.markDone()
			return
		}
	}
}

func (p *Proxy) forwardFromAgent() {
	defer p.wg.Done()

	reader := bufio.NewReader(p.stdout)
	encoder := json.NewEncoder(os.Stdout)

	for {
		select {
		case <-p.done:
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				p.markDone()
			}
			return
		}

		var msg JSONRPCMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		p.extractSessionID(&msg)

		if err := encoder.Encode(&msg); err != nil {
			p.markDone()
			return
		}
	}
}

func (p *Proxy) extractSessionID(msg *JSONRPCMessage) {
	if msg.ID != nil && msg.Result != nil {
		var result SessionNewResult
		if err := json.Unmarshal(msg.Result, &result); err == nil && result.SessionID != "" {
			p.sessionMux.Lock()
			p.sessionID = result.SessionID
			p.sessionMux.Unlock()
		}
	}
}

func (p *Proxy) InjectNotification(method string, params any) error {
	p.sessionMux.RLock()
	sessionID := p.sessionID
	p.sessionMux.RUnlock()

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
	}

	if sessionID != "" || params != nil {
		paramMap := make(map[string]any)
		if sessionID != "" {
			paramMap["sessionId"] = sessionID
		}
		if params != nil {
			switch v := params.(type) {
			case map[string]any:
				for k, val := range v {
					paramMap[k] = val
				}
			default:
				paramMap["params"] = params
			}
		}
		rawParams, err := json.Marshal(paramMap)
		if err != nil {
			return fmt.Errorf("marshaling notification params: %w", err)
		}
		msg.Params = rawParams
	}

	return json.NewEncoder(p.stdin).Encode(&msg)
}

func (p *Proxy) SessionID() string {
	p.sessionMux.RLock()
	defer p.sessionMux.RUnlock()
	return p.sessionID
}

func (p *Proxy) Shutdown() {
	p.markDone()

	if p.cancel != nil {
		p.cancel()
	}

	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Signal(syscall.SIGTERM)
	}
}

func (p *Proxy) markDone() {
	p.doneOnce.Do(func() {
		close(p.done)
	})
}

func (p *Proxy) agentDone() <-chan error {
	ch := make(chan error, 1)
	go func() {
		err := p.cmd.Wait()
		ch <- err
	}()
	return ch
}
