package rbac

import (
	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
)

// Require returns middleware enforcing a global permission.
func (s *Service) Require(perm string) gin.HandlerFunc {
	return s.requireScoped(perm, func(*gin.Context) Scope { return GlobalScope })
}

// RequireServer enforces perm scoped to the server addressed by the :id
// path parameter, so per-server and per-server-group grants apply.
func (s *Service) RequireServer(perm string) gin.HandlerFunc {
	return s.requireScoped(perm, func(c *gin.Context) Scope {
		return ServerScope(c.Param("id"))
	})
}

func (s *Service) requireScoped(perm string, scopeOf func(*gin.Context) Scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := authn.FromContext(c.Request.Context())
		if !ok {
			httpx.Unauthorized(c, "authentication required")
			return
		}
		allowed, err := s.Check(c.Request.Context(), p.UserID, perm, scopeOf(c))
		if err != nil {
			_ = c.Error(err)
			httpx.Internal(c)
			return
		}
		if !allowed {
			httpx.Forbidden(c)
			return
		}
		c.Next()
	}
}
