package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidateBind(t *testing.T) {
	for _, address := range []string{":8080", "0.0.0.0:8080", "192.168.1.2:8080", "[::1]:8080"} {
		if ValidateBind(address, false, "") == nil {
			t.Fatalf("unsafe bind accepted: %s", address)
		}
	}
	if ValidateBind("127.0.0.1:8080", true, "") == nil {
		t.Fatal("missing login accepted")
	}
	if err := ValidateBind("127.0.0.1:8080", true, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.Use(RequireIdentity(true, "owner@example.com"))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, tc := range []struct {
		login string
		want  int
	}{
		{"", http.StatusForbidden},
		{"stranger@example.com", http.StatusForbidden},
		{"owner@example.com", http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.login != "" {
			req.Header.Set("Tailscale-User-Login", tc.login)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("login=%q status=%d want=%d", tc.login, response.Code, tc.want)
		}
	}
	local := httptest.NewRecorder()
	router.ServeHTTP(local, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if local.Code != http.StatusNoContent {
		t.Fatalf("local health status=%d", local.Code)
	}
}
