package ipc

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/crmne/hyprmoncfg/internal/appstatus"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

const ProtocolVersion = 1

const (
	MethodStatus    = "status"
	MethodSubscribe = "subscribe"
	MethodPreview   = "preview"
	MethodConfirm   = "confirm"
	MethodRevert    = "revert"
	MethodSave      = "save"
	MethodDelete    = "delete"
)

const EventStatus = "status"

var ErrTransactionUnavailable = errors.New("interactive preview is no longer available")

type Request struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocol_version"`
	ID              string          `json:"id"`
	Method          string          `json:"method"`
	Params          json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocol_version"`
	ID              string          `json:"id"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *ResponseError  `json:"error,omitempty"`
}

type Event struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocol_version"`
	Event           string          `json:"event"`
	Data            json.RawMessage `json:"data,omitempty"`
}

type ResponseError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type PreviewParams struct {
	Profile                 *profile.Profile `json:"profile,omitempty"`
	ProfileName             string           `json:"profile_name,omitempty"`
	AllowUnmanagedOverwrite bool             `json:"allow_unmanaged_overwrite,omitempty"`
	TimeoutSeconds          int              `json:"timeout_seconds,omitempty"`
}

type TransactionParams struct {
	TransactionID string `json:"transaction_id"`
}

type SaveParams struct {
	Profile profile.Profile `json:"profile"`
}

type DeleteParams struct {
	Name string `json:"name"`
}

type Transaction struct {
	ID       string          `json:"id"`
	Profile  profile.Profile `json:"profile"`
	Deadline time.Time       `json:"deadline"`
}

type Handler interface {
	Status() (appstatus.Document, error)
	Preview(owner string, params PreviewParams) (Transaction, error)
	Confirm(owner string, params TransactionParams) error
	Revert(owner string, params TransactionParams) error
	Save(params SaveParams) error
	Delete(params DeleteParams) error
	Disconnect(owner string)
}
