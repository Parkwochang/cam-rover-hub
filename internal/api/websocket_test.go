package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestActivationACKIsCorrelatedAndKeepsRoverStopped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	motor := &stoppedMotor{}
	c := control.New(motor)
	defer c.Close()
	c.SetGuards(func() bool { return true }, nil)
	a := &API{Control: c}
	router := gin.New()
	a.Register(router)
	server := httptest.NewServer(router)
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(time.Second))
	var reply map[string]any
	if err := ws.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []map[string]any{
		{"type": "supervise", "active": true, "request_id": 11},
		{"type": "activate", "speed": 85, "request_id": 12},
		{"type": "supervise", "active": true, "request_id": 13},
		{"type": "activate", "speed": 0, "request_id": 14},
	} {
		if err := ws.WriteJSON(msg); err != nil {
			t.Fatal(err)
		}
		if err := ws.ReadJSON(&reply); err != nil {
			t.Fatal(err)
		}
		if reply["request_id"] != float64(msg["request_id"].(int)) || reply["command"] != msg["type"] {
			t.Fatalf("uncorrelated response: %v", reply)
		}
		if msg["request_id"] == 14 {
			if reply["type"] != "error" {
				t.Fatal("invalid speed accepted")
			}
		} else if reply["type"] != "ack" {
			t.Fatalf("activation rejected: %v", reply)
		}
	}
	if c.Status().Direction != "stop" {
		t.Fatal("activation moved rover")
	}
	// Finish disconnection before inspecting this simple fake's command slice.
	ws.WriteJSON(map[string]any{"type": "stop", "request_id": 15})
	if err := ws.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
}
