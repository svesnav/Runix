package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/httpx"
)

// Middleware authenticates every request with a Bearer credential (JWT
// access token or PAT). WebSocket endpoints may pass the token via the
// access_token query parameter because browsers cannot set headers on WS
// upgrade requests; those tokens are short-lived JWTs.
func Middleware(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		bearer := bearerToken(c)
		if bearer == "" {
			httpx.Unauthorized(c, "missing bearer token")
			return
		}
		principal, err := svc.AuthenticateAccess(c.Request.Context(), bearer)
		if err != nil {
			httpx.Unauthorized(c, "invalid or expired credentials")
			return
		}
		ctx := authn.WithPrincipal(c.Request.Context(), principal)
		ctx = authn.WithRequestMeta(ctx, authn.RequestMeta{
			IP:        c.ClientIP(),
			UserAgent: c.Request.UserAgent(),
			RequestID: c.GetString("request_id"),
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if isWebSocketUpgrade(c) {
		return c.Query("access_token")
	}
	return ""
}

func isWebSocketUpgrade(c *gin.Context) bool {
	return strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
}
