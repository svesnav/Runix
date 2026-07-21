package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/platform/httpx"
)

const maxBackupBytes = 16 << 20

type Handler struct {
	svc     *Service
	auditor *audit.Service
}

func NewHandler(svc *Service, auditor *audit.Service) *Handler {
	return &Handler{svc: svc, auditor: auditor}
}

// Export streams the configuration as a downloadable JSON document.
func (h *Handler) Export(c *gin.Context) {
	doc, err := h.svc.Export(c.Request.Context())
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "backup.export", TargetType: "backup", Err: err,
	})
	if err != nil {
		_ = c.Error(err)
		httpx.Internal(c)
		return
	}
	raw, err := Marshal(doc)
	if err != nil {
		_ = c.Error(err)
		httpx.Internal(c)
		return
	}
	name := fmt.Sprintf("runix-backup-%s.json", time.Now().UTC().Format("20060102-150405"))
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/json", raw)
}

// Import accepts a previously exported document.
func (h *Handler) Import(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBackupBytes))
	if err != nil {
		httpx.BadRequest(c, "could not read the request body")
		return
	}
	var doc Document
	if err := json.Unmarshal(body, &doc); err != nil {
		httpx.BadRequest(c, "not a valid Runix backup document")
		return
	}
	report, err := h.svc.Import(c.Request.Context(), doc)
	h.auditor.Write(c.Request.Context(), audit.Record{
		Action: "backup.import", TargetType: "backup", New: report, Err: err,
	})
	if err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, report)
}

func RegisterRoutes(r gin.IRouter, h *Handler, requirePerm func(string) gin.HandlerFunc) {
	r.GET("/backup/export", requirePerm("backup.create"), h.Export)
	r.POST("/backup/import", requirePerm("backup.restore"), h.Import)
}
