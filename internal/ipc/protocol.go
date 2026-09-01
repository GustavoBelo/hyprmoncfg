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

	// Console mode. Entering ends the desktop session, so it is armed here
	// rather than done outright: the daemon announces it, waits, and can be
	// called off -- from the panel, the CLI, or by switching the pad back off.
	MethodConsoleStatus = "console.status"
	MethodConsoleEnter  = "console.enter"
	MethodConsoleCancel = "console.cancel"
	// MethodConsoleConfigure edits console mode from a panel, so the settings
	// are not reachable only from a terminal.
	MethodConsoleConfigure = "console.configure"
)

// ConsoleEnterParams says what asked for the session, which lands in the log.
type ConsoleEnterParams struct {
	Trigger string `json:"trigger,omitempty"`
	// GraceMS overrides how long the countdown runs before the desktop closes.
	// Zero means the daemon's own default, which is longer for an entry nobody
	// asked for than for one that was asked for outright.
	//
	// Optional, so a client that has never heard of it -- or a daemon that has
	// not -- still agrees with the other end about what happens.
	GraceMS int `json:"grace_ms,omitempty"`
}

// ConsoleState is the daemon's answer about console mode.
//
// It is much smaller than the couch-mode state it replaces. There is no session
// to report on: once the console starts, this daemon's compositor is gone and
// the state that matters is the hosting session's, not ours.
type ConsoleState struct {
	// Configured is whether a TV has been chosen.
	Configured bool `json:"configured"`
	// Hosted is whether this session can switch at all. Without a hosting
	// session there is no way back, so entering would strand the user.
	Hosted bool `json:"hosted"`
	// Ready is whether everything a console session needs is present.
	Ready bool `json:"ready"`
	// Arming is whether an entry has been announced and is counting down.
	Arming      bool   `json:"arming"`
	TVName      string `json:"tv_name,omitempty"`
	Trigger     bool   `json:"trigger"`
	Controllers int    `json:"controllers"`
	// Problems is what the doctor would say, so a panel can show it without
	// running a command.
	Problems []string `json:"problems,omitempty"`
	// MissingSessionEnv names graphical-session variables the daemon still
	// cannot see. It is empty on a healthy system; when it is not, anything the
	// daemon launches starts without a session and dies silently.
	MissingSessionEnv []string `json:"missing_session_env,omitempty"`

	// The rest is what an editor needs: the current settings, and the choices
	// they can be set to. A panel should not have to know which connectors
	// exist or which session entries are installed.
	Boot           string `json:"boot,omitempty"`
	DesktopSession string `json:"desktop_session,omitempty"`
	// Displays are the connectors a console session could play on, newest read
	// from the compositor rather than remembered.
	Displays []ConsoleDisplay `json:"displays,omitempty"`
	// DesktopSessions are the session entries that could be returned to. A
	// hosting session is never among them: it would host itself.
	DesktopSessions []string `json:"desktop_sessions,omitempty"`
	// BootModes are the accepted values for Boot, so the panel does not carry
	// its own copy of a list that lives in Go.
	BootModes []string `json:"boot_modes,omitempty"`
}

// ConsoleDisplay is one display a console session could play on.
type ConsoleDisplay struct {
	Connector   string `json:"connector"`
	Description string `json:"description,omitempty"`
}

// ConsoleConfigureParams edits console mode. Every field is optional: a panel
// changing one setting should not have to send back the others, and a field it
// does not know about must not be cleared by its silence.
type ConsoleConfigureParams struct {
	TVName         *string `json:"tv_name,omitempty"`
	Boot           *string `json:"boot,omitempty"`
	DesktopSession *string `json:"desktop_session,omitempty"`
	Trigger        *bool   `json:"trigger,omitempty"`
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

	// ConsoleStatus, ConsoleEnter and ConsoleCancel drive console mode. The
	// daemon owns the countdown so it survives whatever asked for the entry:
	// the panel can close, the shell can exit, and the user still gets their
	// warning and their chance to call it off.
	ConsoleStatus() (ConsoleState, error)
	ConsoleEnter(params ConsoleEnterParams) error
	ConsoleCancel() error
	ConsoleConfigure(params ConsoleConfigureParams) error
	Disconnect(owner string)
}
