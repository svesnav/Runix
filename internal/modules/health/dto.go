package health

import "github.com/runix/runix/internal/platform/version"

const (
	statusOK   = "ok"
	statusFail = "fail"
)

type LivenessResponse struct {
	Status        string       `json:"status"`
	Version       version.Info `json:"version"`
	UptimeSeconds float64      `json:"uptimeSeconds"`
}

type CheckResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type ReadinessResponse struct {
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks"`
}
