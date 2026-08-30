package ipc

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/crmne/hyprmoncfg/internal/appstatus"
	"github.com/crmne/hyprmoncfg/internal/profile"
)

// ProtocolVersion is the newest protocol this build speaks. Bump it when the
// wire format gains something a client has to know about.
//
// MinProtocolVersion is the oldest one it still answers. Clients update on
// their own schedule -- the Omarchy panel is a separate git checkout, and the
// daemon keeps running its old binary until someone restarts the service -- so
// a new daemon has to keep serving older clients. The server answers in the
// version the client asked in, which leaves an older client seeing exactly the
// replies it expects, and reports its own newest version separately so a client
// that cares can adapt.
const (
	ProtocolVersion    = 1
	MinProtocolVersion = 1
)

const (
	MethodStatus      = "status"
	MethodSubscribe   = "subscribe"
	MethodEditor      = "editor_state"
	MethodEdit        = "edit_profile"
	MethodPreview     = "preview"
	MethodConfirm     = "confirm"
	MethodCommit      = "commit"
	MethodRevert      = "revert"
	MethodSave        = "save"
	MethodDelete      = "delete"
	MethodManage      = "manage"
	MethodUnmanage    = "unmanage"
	MethodProfileAuto = "set_profile_auto"

	// Couch mode. The session lives in the daemon, so the TUI, the panel and
	// the CLI all drive it from here rather than each spawning their own
	// detached process and racing over the same displays.
	MethodCouchStatus = "couch.status"
	MethodCouchStart  = "couch.start"
	MethodCouchStop   = "couch.stop"
)

// CouchStartParams says what asked for the session, which lands in the log.
type CouchStartParams struct {
	Trigger string `json:"trigger,omitempty"`
}

// CouchState is the daemon's answer about the console session.
type CouchState struct {
	Phase       string `json:"phase"`
	Active      bool   `json:"active"`
	StartedAt   string `json:"started_at,omitempty"`
	Duration    string `json:"duration,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Controllers int    `json:"controllers"`
	Enabled     bool   `json:"enabled"`
	Configured  bool   `json:"configured"`
	Managed     bool   `json:"managed"`
	TVName      string `json:"tv_name,omitempty"`
	Mode        string `json:"mode,omitempty"`
	HDR         bool   `json:"hdr,omitempty"`
	VRR         bool   `json:"vrr,omitempty"`
	// MissingSessionEnv names graphical-session variables the daemon still
	// cannot see. It is empty on a healthy system; when it is not, a session
	// started by a trigger puts the TV layout up and then launches a Steam
	// that exits at once, silently.
	MissingSessionEnv []string `json:"missing_session_env,omitempty"`
	// UnavailableHooks names hooks the daemon cannot run, as the daemon sees
	// it. Asking the CLI instead answers for the wrong process: a user shell
	// finds the Omarchy helpers on PATH whether or not the daemon can.
	UnavailableHooks []string `json:"unavailable_hooks,omitempty"`
}

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
	Type            string `json:"type"`
	ProtocolVersion int    `json:"protocol_version"`
	// ServerProtocolVersion is the newest version the daemon speaks, whatever
	// version this reply is written in. A client reads it to feature-detect
	// instead of guessing from the daemon's release number.
	ServerProtocolVersion int             `json:"server_protocol_version,omitempty"`
	ID                    string          `json:"id"`
	Result                json.RawMessage `json:"result,omitempty"`
	Error                 *ResponseError  `json:"error,omitempty"`
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
	Profile        *profile.Profile `json:"profile,omitempty"`
	ProfileName    string           `json:"profile_name,omitempty"`
	TimeoutSeconds int              `json:"timeout_seconds,omitempty"`
	SaveOnCommit   bool             `json:"save_on_commit,omitempty"`
}

type EditParams struct {
	Profile profile.Profile    `json:"profile"`
	Edit    profile.EditorEdit `json:"edit"`
}

type TransactionParams struct {
	TransactionID string `json:"transaction_id"`
}

type CommitParams struct {
	TransactionID string `json:"transaction_id"`
	Save          bool   `json:"save"`
}

type SaveParams struct {
	Profile profile.Profile `json:"profile"`
}

type DeleteParams struct {
	Name string `json:"name"`
}

type ProfileAutoParams struct {
	Enabled bool `json:"enabled"`
}

type Transaction struct {
	ID       string          `json:"id"`
	Profile  profile.Profile `json:"profile"`
	Deadline time.Time       `json:"deadline"`
}

type Handler interface {
	Status() (appstatus.Document, error)
	EditorState() (appstatus.EditorDocument, error)
	EditProfile(params EditParams) (appstatus.EditorDraft, error)
	Preview(owner string, params PreviewParams) (Transaction, error)
	Confirm(owner string, params TransactionParams) error
	Commit(owner string, params CommitParams) error
	Revert(owner string, params TransactionParams) error
	Save(params SaveParams) error
	Delete(params DeleteParams) error
	// Manage and Unmanage move monitor configuration between hyprmoncfg and
	// Hyprland's own config. Unmanage has to stop the daemon applying as well as
	// take the include out, or the next monitor event puts it straight back.
	Manage() error
	Unmanage() error
	SetProfileAuto(params ProfileAutoParams) error

	// CouchStatus, CouchStart and CouchStop drive the console session. The
	// daemon owns it so it survives the TUI closing and can be reconciled if
	// the daemon itself is killed.
	CouchStatus() (CouchState, error)
	CouchStart(params CouchStartParams) error
	CouchStop() error
	Disconnect(owner string)
}
