package rover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestExistingFirmwareControlAndNetworkContract(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("X-Rover-Token") != "secret" || r.Header.Get("X-Rover-Controller") != "hub" {
			t.Errorf("missing rover headers")
		}
		switch r.URL.Path {
		case "/api/network":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"mode":"sta","sta_ip":"192.168.1.30","sta_configured":true}`))
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
		case "/api/move":
			w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := New(server.URL, "secret")
	if err := client.Move(context.Background(), "forward-left"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetNetwork(context.Background(), "sta", "home", "password123"); err != nil {
		t.Fatal(err)
	}
	status, err := client.Network(context.Background())
	if err != nil || !status.STAConfigured || status.STAIP != "192.168.1.30" {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if paths[0] != "GET /api/move?direction=forward-left" || paths[1] != "POST /api/network" {
		t.Fatalf("unexpected request sequence: %v", paths)
	}
}

func TestRedirectCannotForwardControlHeaders(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := New(source.URL, "synthetic-test-token")
	if err := client.Move(context.Background(), "stop"); err == nil {
		t.Fatal("redirect was accepted")
	}
	if received.Load() != 0 {
		t.Fatal("control headers were sent to redirect target")
	}
}
