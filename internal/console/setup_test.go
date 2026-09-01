package console

import "testing"

// Asking "which session is running" inside a hosted session answers with the
// hosting entry. Recording that would make the wrapper host itself and the user
// would never reach a desktop.
func TestHostsConsoleRecognisesAHostingEntry(t *testing.T) {
	hosting := Entry{Exec: []string{"/home/u/.local/bin/hyprmoncfg", "console", "session"}}
	if !HostsConsole(hosting) {
		t.Error("a hosting entry was not recognised")
	}
	for _, plain := range []Entry{
		{Exec: []string{"uwsm", "start", "-g", "-1", "-e", "-D", "Hyprland", "hyprland.desktop"}},
		{Exec: []string{"start-gamescope-session"}},
		{Exec: []string{"hyprmoncfg", "console", "enter"}},
		{Exec: []string{"console"}},
		{Exec: nil},
	} {
		if HostsConsole(plain) {
			t.Errorf("%v was mistaken for a hosting entry", plain.Exec)
		}
	}
}

// Running setup twice must not stack the suffix.
func TestHostingEntryNameDoesNotStack(t *testing.T) {
	first := HostingEntryName("Omarchy (Hyprland uwsm)")
	if first != "Omarchy (Hyprland uwsm) (console switch)" {
		t.Fatalf("name = %q", first)
	}
	if again := HostingEntryName(first); again != first {
		t.Errorf("running setup twice gave %q", again)
	}
}

func TestEntryContentCarriesTheDesktopIdentity(t *testing.T) {
	body := EntryContent("X", "hyprmoncfg console session", []string{"Hyprland"})
	if !contains(body, "DesktopNames=Hyprland") || !contains(body, "Exec=hyprmoncfg console session") {
		t.Fatalf("entry = %q", body)
	}
	// An entry with no names still has to declare something, or portals and
	// autostart rules key off an empty desktop.
	if !contains(EntryContent("X", "cmd", nil), "DesktopNames=Hyprland") {
		t.Error("an entry with no names declared none")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// Reading the Exec line is not enough: a hosting entry may point at a wrapper
// script, and then it looks like an ordinary session and offers itself as
// somewhere to come back to -- which would host itself forever.
func TestHostsConsoleTrustsTheMarkerWhateverTheCommand(t *testing.T) {
	viaScript := Entry{Exec: []string{"/home/u/.local/share/some/wrapper.sh"}, Hosting: true}
	if !HostsConsole(viaScript) {
		t.Error("a marked entry was not recognised as hosting")
	}
	if HostsConsole(Entry{Exec: []string{"/home/u/.local/share/some/wrapper.sh"}}) {
		t.Error("an unmarked script was mistaken for a hosting entry")
	}
}

func TestEntryContentCarriesTheMarker(t *testing.T) {
	if !contains(EntryContent("X", "hyprmoncfg console session", nil), HostingMarker+"=true") {
		t.Error("the generated entry does not mark itself as hosting")
	}
}
