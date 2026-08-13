package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crmne/hyprmoncfg/internal/apply"
	"github.com/crmne/hyprmoncfg/internal/appstatus"
)

type Client struct {
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
	mu      sync.Mutex
	nextID  atomic.Uint64
}

func Dial(ctx context.Context, path string) (*Client, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:    conn,
		encoder: json.NewEncoder(conn),
		decoder: json.NewDecoder(conn),
	}
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Status(ctx context.Context) (appstatus.Document, error) {
	var result appstatus.Document
	err := c.call(ctx, MethodStatus, nil, &result)
	return result, err
}

func (c *Client) Subscribe(ctx context.Context) (appstatus.Document, error) {
	var result appstatus.Document
	err := c.call(ctx, MethodSubscribe, nil, &result)
	return result, err
}

func (c *Client) Preview(ctx context.Context, params PreviewParams) (Transaction, error) {
	var result Transaction
	err := c.call(ctx, MethodPreview, params, &result)
	return result, err
}

func (c *Client) Confirm(ctx context.Context, transactionID string) error {
	return c.call(ctx, MethodConfirm, TransactionParams{TransactionID: transactionID}, nil)
}

func (c *Client) Revert(ctx context.Context, transactionID string) error {
	return c.call(ctx, MethodRevert, TransactionParams{TransactionID: transactionID}, nil)
}

func (c *Client) Save(ctx context.Context, params SaveParams) error {
	return c.call(ctx, MethodSave, params, nil)
}

func (c *Client) Delete(ctx context.Context, name string) error {
	return c.call(ctx, MethodDelete, DeleteParams{Name: name}, nil)
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	if c == nil || c.conn == nil {
		return errors.New("IPC client is not connected")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	id := fmt.Sprintf("%d", c.nextID.Add(1))
	request := Request{
		Type:            "request",
		ProtocolVersion: ProtocolVersion,
		ID:              id,
		Method:          method,
	}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return err
		}
		request.Params = encoded
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return err
		}
		defer c.conn.SetDeadline(time.Time{})
	}
	if err := c.encoder.Encode(request); err != nil {
		return err
	}

	for {
		var envelope struct {
			Type string `json:"type"`
		}
		var raw json.RawMessage
		if err := c.decoder.Decode(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		if envelope.Type == "event" {
			continue
		}

		var response Response
		if err := json.Unmarshal(raw, &response); err != nil {
			return err
		}
		if response.ID != id {
			return fmt.Errorf("unexpected IPC response id %q (wanted %q)", response.ID, id)
		}
		if response.ProtocolVersion != ProtocolVersion {
			return fmt.Errorf("unsupported IPC protocol version %d", response.ProtocolVersion)
		}
		if response.Error != nil {
			return decodeResponseError(response.Error)
		}
		if result != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return err
			}
		}
		return nil
	}
}

func decodeResponseError(responseErr *ResponseError) error {
	if responseErr == nil {
		return nil
	}
	if responseErr.Code == "unmanaged_monitor_config" {
		path, _ := responseErr.Data["path"].(string)
		alternative, _ := responseErr.Data["alternative_path"].(string)
		return &apply.UnmanagedMonitorConfigError{
			Path:            path,
			AlternativePath: alternative,
		}
	}
	if responseErr.Code == "transaction_unavailable" {
		return fmt.Errorf("%w: %s", ErrTransactionUnavailable, responseErr.Message)
	}
	return fmt.Errorf("%s", responseErr.Message)
}
