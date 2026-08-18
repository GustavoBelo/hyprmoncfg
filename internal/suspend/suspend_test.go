package suspend

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestFromSignal(t *testing.T) {
	tests := []struct {
		name         string
		signal       *dbus.Signal
		wantSleeping bool
		wantOK       bool
	}{
		{name: "nil signal"},
		{
			name: "sleeping",
			signal: &dbus.Signal{
				Path: loginPath,
				Name: sleepSignal,
				Body: []any{true},
			},
			wantSleeping: true,
			wantOK:       true,
		},
		{
			name: "woke",
			signal: &dbus.Signal{
				Path: loginPath,
				Name: sleepSignal,
				Body: []any{false},
			},
			wantOK: true,
		},
		{
			name: "wrong signal name",
			signal: &dbus.Signal{
				Path: loginPath,
				Name: loginInterface + ".SessionNew",
				Body: []any{true},
			},
		},
		{
			name: "wrong path",
			signal: &dbus.Signal{
				Path: dbus.ObjectPath("/org/freedesktop/login1/session/_31"),
				Name: sleepSignal,
				Body: []any{true},
			},
		},
		{
			name: "empty body",
			signal: &dbus.Signal{
				Path: loginPath,
				Name: sleepSignal,
			},
		},
		{
			name: "non-bool body",
			signal: &dbus.Signal{
				Path: loginPath,
				Name: sleepSignal,
				Body: []any{"true"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sleeping, ok := fromSignal(tt.signal)
			if sleeping != tt.wantSleeping || ok != tt.wantOK {
				t.Fatalf("fromSignal() = (%t, %t), want (%t, %t)", sleeping, ok, tt.wantSleeping, tt.wantOK)
			}
		})
	}
}
