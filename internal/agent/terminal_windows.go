//go:build windows

package agent

import (
	"fmt"

	rt "github.com/runix/runix/internal/domain/runtime"
)

// Windows host terminals need ConPTY plumbing; managed servers are
// Linux-first, so this is a deliberate gap rather than an accident.
func openHostTerminal(cols, rows uint16) (rt.Terminal, error) {
	return nil, fmt.Errorf("%w: host terminal on windows", rt.ErrNotSupported)
}
