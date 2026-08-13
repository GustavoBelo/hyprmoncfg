package writerlock

import (
	"errors"
	"testing"
)

func TestTryAcquireIsExclusiveAndReleasesOnClose(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, err := TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := TryAcquire(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquisition error = %v, want ErrBusy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := TryAcquire()
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = second.Close()
}
