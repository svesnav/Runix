package agent

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/runix/runix/internal/protocol"
)

// fsErr is called as fsErr(op()) throughout the handlers, so a nil error
// must map to nil rather than panicking on err.Error().
func TestFsErrNilIsNil(t *testing.T) {
	if got := fsErr(nil); got != nil {
		t.Fatalf("fsErr(nil) = %+v, want nil", got)
	}
}

func TestFsErrCodes(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fs.ErrNotExist, protocol.CodeNotFound},
		{fs.ErrPermission, protocol.CodeInvalid},
		{fs.ErrExist, protocol.CodeInvalid},
		{errors.New("disk on fire"), protocol.CodeInternal},
	}
	for _, c := range cases {
		got := fsErr(c.err)
		if got == nil || got.Code != c.want {
			t.Errorf("fsErr(%v) = %+v, want code %s", c.err, got, c.want)
		}
	}
}

func TestCleanAbsRejectsRelative(t *testing.T) {
	for _, p := range []string{"", "relative/path", "./x"} {
		if _, e := cleanAbs(p); e == nil {
			t.Errorf("cleanAbs(%q) accepted a non-absolute path", p)
		}
	}
	// "Absolute" is host-defined (agents run on Linux; tests may run on
	// Windows), so build the sample from the host's own temp dir.
	abs := filepath.Join(t.TempDir(), "sub", "..", "file.txt")
	if _, e := cleanAbs(abs); e != nil {
		t.Errorf("cleanAbs(%q) rejected a valid absolute path: %v", abs, e)
	}
}
