package console

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackerWaitsForMinimumUsage(t *testing.T) {
	tracker := &ControllerTracker{}
	base := time.Unix(1000, 0)

	if tracker.Poll(base, 1) {
		t.Fatal("first poll with a controller connected must not trigger")
	}
	if tracker.UsageSeconds != 0 {
		t.Fatalf("first poll should not accumulate usage, got %d", tracker.UsageSeconds)
	}

	if tracker.Poll(base.Add(30*time.Second), 1) {
		t.Fatal("usage below minimum must not trigger")
	}
	if tracker.UsageSeconds != 30 {
		t.Fatalf("usage = %d, want 30", tracker.UsageSeconds)
	}

	if tracker.Poll(base.Add(40*time.Second), 0) {
		t.Fatal("controllers off before minimum usage must not trigger")
	}

	if tracker.Poll(base.Add(50*time.Second), 1) {
		t.Fatal("controllers back on must not trigger")
	}
	if tracker.UsageSeconds != 40 {
		t.Fatalf("usage = %d, want 40", tracker.UsageSeconds)
	}
}

func TestTrackerDebounce(t *testing.T) {
	tracker := &ControllerTracker{}
	base := time.Unix(2000, 0)

	tracker.Poll(base, 1)
	tracker.Poll(base.Add(ControllerMinUsageSeconds*time.Second), 1)
	if got := tracker.UsageSeconds; got < ControllerMinUsageSeconds {
		t.Fatalf("precondition failed: usage %d", got)
	}

	if tracker.Poll(base.Add(70*time.Second), 0) {
		t.Fatal("first poll without controllers only starts the debounce window")
	}
	if tracker.Poll(base.Add(75*time.Second), 0) {
		t.Fatal("within debounce window must not trigger")
	}
	if !tracker.Poll(base.Add(85*time.Second), 0) {
		t.Fatal("after debounce window must trigger")
	}
}

func TestTrackerControllerReturningResetsDebounce(t *testing.T) {
	tracker := &ControllerTracker{}
	base := time.Unix(3000, 0)

	tracker.Poll(base, 1)
	tracker.Poll(base.Add(60*time.Second), 1)
	tracker.Poll(base.Add(62*time.Second), 0)
	tracker.Poll(base.Add(68*time.Second), 0)
	tracker.Poll(base.Add(69*time.Second), 1)
	if tracker.Poll(base.Add(72*time.Second), 0) {
		t.Fatal("controller came back inside the debounce window; timer restarts")
	}
	if !tracker.Poll(base.Add(83*time.Second), 0) {
		t.Fatal("full debounce after reconnect must trigger")
	}
}

func TestConnectedControllersNeverNegative(t *testing.T) {
	if ConnectedControllers() < 0 {
		t.Fatal("negative controller count")
	}
}

// The sysfs bitmap is space-separated hex words, most significant first, so
// bit 0x130 lands in the fifth word counting back from the end.
func TestKeyBitSetReadsGamepadButton(t *testing.T) {
	cases := []struct {
		name   string
		bitmap string
		want   bool
	}{
		// A real Xbox pad: BTN_SOUTH..BTN_THUMBR set in the 0x130 block.
		{"gamepad", "3 0 0 7 0 0 0 0 0 0", false},
		{"gamepad bit set", "1000000000000 0 0 0 0", true},
		// A keyboard from this host: four words, so bit 304 is out of range.
		{"keyboard", "1000000000007 ff800000000007ff febeffdfffefffff fffffffffffffffe", false},
		// A mouse from this host: nine words, but the 0x130 word is zero.
		{"mouse", "7e40000 0 800000000000 0 0 1400b00100000 300180001100800 e000000000000 2", false},
		{"empty", "", false},
		{"garbage", "not-hex", false},
	}
	for _, tc := range cases {
		if got := keyBitSet(tc.bitmap, btnGamepad); got != tc.want {
			t.Fatalf("%s: keyBitSet(%q) = %v, want %v", tc.name, tc.bitmap, got, tc.want)
		}
	}
}

func TestConnectedControllersCountsEvdevGamepads(t *testing.T) {
	root := t.TempDir()
	write := func(dir, bitmap string) {
		t.Helper()
		path := filepath.Join(root, dir, "device", "capabilities")
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(path, "key"), []byte(bitmap+"\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("event0", "1000000000007 ff800000000007ff febeffdfffefffff fffffffffffffffe") // keyboard
	write("event1", "1000000000000 0 0 0 0")                                            // gamepad
	write("event2", "1000000000000 0 0 0 0")                                            // second gamepad
	write("mouse0", "1000000000000 0 0 0 0")                                            // not an event node

	oldRoot, oldGlob := inputClassRoot, legacyJoystickGlob
	inputClassRoot = root
	legacyJoystickGlob = filepath.Join(root, "nonexistent-js*")
	defer func() { inputClassRoot, legacyJoystickGlob = oldRoot, oldGlob }()

	if got := ConnectedControllers(); got != 2 {
		t.Fatalf("expected 2 gamepads, got %d", got)
	}
}

// joydev being loaded is not enough for /dev/input/js* to exist; the glob is
// empty on this host while a controller would still show up in evdev.
func TestConnectedControllersFallsBackToLegacyNodes(t *testing.T) {
	root := t.TempDir()
	jsDir := t.TempDir()
	for _, name := range []string{"js0", "js1"} {
		if err := os.WriteFile(filepath.Join(jsDir, name), nil, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	oldRoot, oldGlob := inputClassRoot, legacyJoystickGlob
	inputClassRoot = filepath.Join(root, "missing")
	legacyJoystickGlob = filepath.Join(jsDir, "js*")
	defer func() { inputClassRoot, legacyJoystickGlob = oldRoot, oldGlob }()

	if got := ConnectedControllers(); got != 2 {
		t.Fatalf("expected the legacy fallback to find 2, got %d", got)
	}
}
