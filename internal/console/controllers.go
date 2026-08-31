package console

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// ControllerDebounceSeconds is how long a pad has to stay gone before a
	// disconnect counts, so a flat battery blinking off does not end a session.
	ControllerDebounceSeconds = 10
	// ControllerMinUsageSeconds is how long a pad has to have been used before
	// its disconnect is allowed to mean anything.
	ControllerMinUsageSeconds = 60
)

type ControllerTracker struct {
	UsageSeconds int
	offSince     time.Time
	lastPoll     time.Time
}

func (t *ControllerTracker) Poll(now time.Time, connected int) bool {
	if !t.lastPoll.IsZero() {
		delta := int(now.Sub(t.lastPoll).Seconds())
		if delta > 0 && connected > 0 {
			t.UsageSeconds += delta
		}
	}
	t.lastPoll = now

	if connected > 0 {
		t.offSince = time.Time{}
		return false
	}
	if t.UsageSeconds < ControllerMinUsageSeconds {
		t.offSince = time.Time{}
		return false
	}
	if t.offSince.IsZero() {
		t.offSince = now
		return false
	}
	return int(now.Sub(t.offSince).Seconds()) >= ControllerDebounceSeconds
}

// btnGamepad is BTN_SOUTH (0x130), the first button of evdev's gamepad block.
// A device that advertises it is a gamepad; nothing else claims that bit.
const btnGamepad = 0x130

// inputClassRoot and legacyJoystickGlob are variables so tests can point them
// at fixtures.
var (
	inputClassRoot     = "/sys/class/input"
	legacyJoystickGlob = "/dev/input/js*"
)

// ConnectedControllers counts attached gamepads.
//
// The legacy /dev/input/js* nodes are not a reliable answer: they only exist
// when the joydev module is loaded and bound, and on this host the glob is
// empty while joydev is loaded. evdev capabilities are what the kernel always
// exposes, so they are the primary source and js* is only a fallback for the
// odd driver that skips evdev.
func ConnectedControllers() int {
	if count, ok := gamepadsFromSysfs(); ok {
		return count
	}
	matches, err := filepath.Glob(legacyJoystickGlob)
	if err != nil {
		return 0
	}
	return len(matches)
}

func gamepadsFromSysfs() (int, bool) {
	entries, err := os.ReadDir(inputClassRoot)
	if err != nil {
		return 0, false
	}
	count := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "event") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(inputClassRoot, entry.Name(), "device", "capabilities", "key"))
		if err != nil {
			continue
		}
		if keyBitSet(string(data), btnGamepad) {
			count++
		}
	}
	return count, true
}

// keyBitSet reads one bit out of a sysfs capability bitmap.
//
// The format is space-separated hex words, most significant first, so the last
// word holds bits 0-63. Word width follows the kernel's long, which is 64 bits
// on every platform this runs on.
func keyBitSet(bitmap string, bit int) bool {
	words := strings.Fields(strings.TrimSpace(bitmap))
	if len(words) == 0 {
		return false
	}
	index := len(words) - 1 - bit/64
	if index < 0 {
		return false
	}
	value, err := strconv.ParseUint(words[index], 16, 64)
	if err != nil {
		return false
	}
	return value&(1<<uint(bit%64)) != 0
}
