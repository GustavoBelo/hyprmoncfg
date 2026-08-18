// Package suspend reports systemd-logind sleep transitions.
package suspend

import (
	"context"

	"github.com/godbus/dbus/v5"
)

const (
	loginPath      = dbus.ObjectPath("/org/freedesktop/login1")
	loginInterface = "org.freedesktop.login1.Manager"
	sleepSignal    = loginInterface + ".PrepareForSleep"
)

// Watch emits true when the system is about to sleep and false once it has
// woken. The channel closes when logind cannot be reached or ctx ends; a
// closed channel means sleep transitions simply go unreported.
func Watch(ctx context.Context) <-chan bool {
	events := make(chan bool, 4)

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		close(events)
		return events
	}

	signalCh := make(chan *dbus.Signal, 8)
	conn.Signal(signalCh)

	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(loginPath),
		dbus.WithMatchInterface(loginInterface),
		dbus.WithMatchMember("PrepareForSleep"),
	); err != nil {
		conn.RemoveSignal(signalCh)
		_ = conn.Close()
		close(events)
		return events
	}

	go func() {
		defer close(events)
		defer conn.Close()
		defer conn.RemoveSignal(signalCh)

		for {
			select {
			case <-ctx.Done():
				return
			case signal, ok := <-signalCh:
				if !ok {
					return
				}
				sleeping, ok := fromSignal(signal)
				if !ok {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case events <- sleeping:
				}
			}
		}
	}()

	return events
}

func fromSignal(signal *dbus.Signal) (bool, bool) {
	if signal == nil || signal.Name != sleepSignal || signal.Path != loginPath {
		return false, false
	}
	if len(signal.Body) == 0 {
		return false, false
	}
	sleeping, ok := signal.Body[0].(bool)
	return sleeping, ok
}
