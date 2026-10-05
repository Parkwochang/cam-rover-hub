package api

import (
	"context"
	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/rover"
	"github.com/Parkwochang/cam-rover-hub/internal/store"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type stoppedMotor struct{ commands []string }

func (m *stoppedMotor) Move(_ context.Context, d string) error {
	m.commands = append(m.commands, d)
	return nil
}
func (m *stoppedMotor) Speed(context.Context, int) error { return nil }

func TestAddressCanBeSavedWhileRoverOfflineAndRejectsSSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	motor := &stoppedMotor{}
	coordinator := control.New(motor)
	defer coordinator.Close()
	a := &API{Rover: rover.New("172.30.1.28", "secret"), Control: coordinator, SettingsDB: db}
	router := gin.New()
	a.Register(router)
	for _, input := range []string{"8.8.8.8", "127.0.0.1", "http://user:secret@172.30.1.22"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/link", strings.NewReader(`{"address":"`+input+`"}`)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe address accepted: %s", input)
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/link", strings.NewReader(`{"address":"cam-rover.local"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	saved, err := store.RoverAddress(context.Background(), db)
	if err != nil || saved != "http://cam-rover.local" {
		t.Fatalf("saved=%q err=%v", saved, err)
	}
	if state := coordinator.Status(); state.Direction != "stop" || state.Mode != "manual" {
		t.Fatalf("unsafe state: %+v", state)
	}
	if len(motor.commands) != 2 || motor.commands[0] != "stop" || motor.commands[1] != "stop" {
		t.Fatalf("commands=%v", motor.commands)
	}
}
