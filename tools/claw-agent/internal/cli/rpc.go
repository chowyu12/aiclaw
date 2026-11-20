package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/server"
)

type frame struct {
	ID     json.RawMessage           `json:"id"`
	Method string                    `json:"method"`
	Params json.RawMessage           `json:"params"`
	Result json.RawMessage           `json:"result"`
	Error  *struct{ Message string } `json:"error"`
}

type connection struct {
	wire      net.Conn
	cancel    context.CancelFunc
	encoder   *json.Encoder
	frames    chan frame
	readError error
	done      chan struct{}
	nextID    int
}

func connect(options server.Options) (*connection, error) {
	client, kernel := net.Pipe()
	srv, err := server.New(options, kernel)
	if err != nil {
		client.Close()
		kernel.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &connection{wire: client, cancel: cancel, encoder: json.NewEncoder(client), frames: make(chan frame, 16), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer kernel.Close()
		_ = srv.Serve(ctx, kernel)
	}()
	go func() {
		defer close(c.frames)
		reader := bufio.NewScanner(client)
		reader.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for reader.Scan() {
			var message frame
			if err := json.Unmarshal(reader.Bytes(), &message); err != nil {
				c.readError = err
				return
			}
			select {
			case c.frames <- message:
			case <-c.done:
				return
			}
		}
		c.readError = reader.Err()
	}()
	return c, nil
}

func (c *connection) close() {
	_ = c.wire.Close()
	c.cancel()
	<-c.done
}

func (c *connection) send(method string, params any) (string, error) {
	c.nextID++
	id := fmt.Sprint(c.nextID)
	err := c.encoder.Encode(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", c.nextID, method, params})
	return id, err
}

func (c *connection) reply(id json.RawMessage, result any) error {
	return c.encoder.Encode(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{"2.0", id, result})
}

func (c *connection) reject(message frame) error {
	return c.encoder.Encode(map[string]any{
		"jsonrpc": "2.0", "id": message.ID,
		"error": map[string]any{"code": -32601, "message": fmt.Sprintf("CLI does not handle %s", message.Method)},
	})
}

func (c *connection) disconnected() error {
	if c.readError != nil {
		return c.readError
	}
	return io.ErrUnexpectedEOF
}

func (c *connection) call(ctx context.Context, method string, params, result any) error {
	id, err := c.send(method, params)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-c.frames:
			if !ok {
				return c.disconnected()
			}
			if message.Method == "" && string(message.ID) == id {
				if message.Error != nil {
					return errors.New(message.Error.Message)
				}
				if result != nil {
					return json.Unmarshal(message.Result, result)
				}
				return nil
			}
			if message.Method != "" && len(message.ID) > 0 {
				if err := c.reject(message); err != nil {
					return err
				}
			}
		}
	}
}
