package security

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ValidateBind rejects direct LAN/tailnet exposure. Identity headers are only
// trustworthy when Tailscale Serve is the sole remote ingress to this socket.
func ValidateBind(address string, requireIdentity bool, allowedLogin string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("LISTEN_ADDR must be 127.0.0.1:port")
	}
	if requireIdentity && strings.TrimSpace(allowedLogin) == "" {
		return errors.New("TAILSCALE_ALLOWED_LOGIN is required")
	}
	return nil
}

func RequireIdentity(enabled bool, allowedLogin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled {
			c.Next()
			return
		}
		login := c.GetHeader("Tailscale-User-Login")
		if login == "" || !strings.EqualFold(login, allowedLogin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Tailscale identity not authorized"})
			return
		}
		c.Next()
	}
}
