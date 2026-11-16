package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// A single local stdio transport. No sampling, elicitation or server-initiated
// execution is offered. Callers serialize tool calls in MCPHub.
type MCPClient struct {
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	in      io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int
	pending map[string]chan rpcReply
	done    chan struct{}
	once    sync.Once
	stderr  *cappedBuffer
}
type rpcReply struct {
	Result json.RawMessage
	Err    error
}

func startMCP(ctx context.Context, cfg ServerConfig) (*MCPClient, error) {
	life, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(life, cfg.Command, cfg.Args...)
	prepareProcess(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		in.Close()
		return nil, err
	}
	c := &MCPClient{cmd: cmd, cancel: cancel, in: in, pending: map[string]chan rpcReply{}, done: make(chan struct{}), stderr: &cappedBuffer{}}
	cmd.Stderr = c.stderr
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		cancel()
		in.Close()
		return nil, err
	}
	go c.read(out)
	result, err := c.call(ctx, "initialize", M{"protocolVersion": "2025-06-18", "capabilities": M{}, "clientInfo": M{"name": "L-Bridge", "version": version}})
	if err == nil {
		var init M
		if json.Unmarshal(result, &init) != nil || str(init, "protocolVersion") == "" {
			err = errors.New("invalid MCP initialize result")
		}
	}
	if err == nil {
		err = c.write(M{"jsonrpc": "2.0", "method": "notifications/initialized"})
	}
	if err != nil {
		c.close()
		return nil, fmt.Errorf("MCP initialize failed: %w", err)
	}
	return c, nil
}
func (c *MCPClient) read(out io.Reader) {
	defer func() {
		c.cancel()
		c.cmd.Wait()
		killDescendants(c.cmd)
		c.once.Do(func() { close(c.done) })
	}()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 65536), 8*1024*1024)
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			return
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 {
				if msg.Method == "ping" {
					c.write(M{"jsonrpc": "2.0", "id": msg.ID, "result": M{}})
				} else {
					c.write(M{"jsonrpc": "2.0", "id": msg.ID, "error": M{"code": -32601, "message": "Server-initiated requests are not supported"}})
				}
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[string(msg.ID)]
		c.mu.Unlock()
		if ch != nil {
			reply := rpcReply{Result: msg.Result}
			if msg.Error != nil {
				reply.Err = errors.New(msg.Error.Message)
			}
			select {
			case ch <- reply:
			default:
			}
		}
	}
}
func (c *MCPClient) write(msg any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.in).Encode(msg)
}
func (c *MCPClient) call(ctx context.Context, method string, params M) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("MCP process disconnected")
	default:
	}
	c.mu.Lock()
	c.next++
	key := fmt.Sprint(c.next)
	number := c.next
	ch := make(chan rpcReply, 1)
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	// A child that stops reading stdin must not hold the caller forever.
	written := make(chan error, 1)
	go func() { written <- c.write(M{"jsonrpc": "2.0", "id": number, "method": method, "params": params}) }()
	select {
	case err := <-written:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		c.close()
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("MCP process disconnected")
	}
	select {
	case res := <-ch:
		return res.Result, res.Err
	case <-ctx.Done():
		// Do not retry an operation whose outcome is unknown. Closing the
		// transport also unblocks a child stuck while writing to stdout.
		c.close()
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("MCP process disconnected")
	}
}
func (c *MCPClient) close() { c.cancel(); c.in.Close() }
func (c *MCPClient) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}
func (c *MCPClient) tools(ctx context.Context) ([]M, error) {
	all := []M{}
	cursor := ""
	seen := map[string]bool{}
	for pages := 0; pages < 20; pages++ {
		params := M{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		b, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var result struct {
			Tools []M    `json:"tools"`
			Next  string `json:"nextCursor"`
		}
		if json.Unmarshal(b, &result) != nil {
			return nil, errors.New("invalid MCP tools/list result")
		}
		all = append(all, result.Tools...)
		if len(all) > 500 {
			return nil, errors.New("MCP server exposes more than 500 tools")
		}
		if result.Next == "" {
			return all, nil
		}
		if seen[result.Next] {
			return nil, errors.New("MCP server repeated pagination cursor")
		}
		seen[result.Next] = true
		cursor = result.Next
	}
	return nil, errors.New("MCP tool pagination exceeded 20 pages")
}
