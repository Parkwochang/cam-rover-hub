package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/rover"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type APConnector interface {
	Connect() error
	Disconnect() error
}

type API struct {
	Rover     *rover.Client
	Control   *control.Coordinator
	AP        APConnector
	nextOwner atomic.Uint64
}

func (a *API) Register(router *gin.Engine) {
	router.GET("/ws", a.websocket)
	router.GET("/api/status", a.status)
	router.POST("/api/mode", a.setMode)
	router.GET("/api/network", a.network)
	router.POST("/api/network", a.setNetwork)
	router.GET("/api/wifi/scan", a.scanStatus)
	router.POST("/api/wifi/scan", a.scan)
	router.POST("/api/link/ap", a.connectAP)
}

func (a *API) status(c *gin.Context) {
	status := a.Control.Status()
	c.JSON(http.StatusOK, gin.H{"mode": status.Mode, "direction": status.Direction, "occupied": status.Occupied, "fault": status.Fault, "rover": a.Rover.Address()})
}

func (a *API) setMode(c *gin.Context) {
	var input struct {
		Mode       string `json:"mode"`
		OperatorID uint64 `json:"operator_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128)
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mode request"})
		return
	}
	if err := a.Control.SetMode(input.OperatorID, input.Mode); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, a.Control.Status())
}

func (a *API) network(c *gin.Context) {
	status, err := a.Rover.Network(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, status)
}

func (a *API) scanStatus(c *gin.Context) {
	status, err := a.Rover.ScanStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, status)
}

func (a *API) scan(c *gin.Context) {
	a.Control.Stop()
	if err := a.Rover.Scan(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"scanning": true})
}

func (a *API) setNetwork(c *gin.Context) {
	var input struct {
		Mode     string `json:"mode"`
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256)
	if err := c.ShouldBindJSON(&input); err != nil || (input.Mode != "ap" && input.Mode != "sta") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid network request"})
		return
	}
	if input.Mode == "sta" && (input.SSID == "") != (input.Password == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSID and password must be provided together"})
		return
	}
	a.Control.Stop()
	if err := a.Rover.SetNetwork(c.Request.Context(), input.Mode, input.SSID, input.Password); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if input.Mode == "ap" {
		go func() {
			if a.AP.Connect() == nil {
				a.Rover.SetAddress("192.168.71.1")
			}
		}()
	} else {
		go a.waitForStation()
	}
	c.JSON(http.StatusAccepted, gin.H{"accepted": true, "phase": "testing"})
}

func (a *API) waitForStation() {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			status, err := a.Rover.Network(ctx)
			cancel()
			if err != nil || status.Mode != "sta" || status.STAIP == "" {
				continue
			}
			candidate := rover.New(status.STAIP, "")
			ctx, cancel = context.WithTimeout(context.Background(), 1500*time.Millisecond)
			_, err = candidate.Network(ctx)
			cancel()
			if err == nil {
				a.Rover.SetAddress(status.STAIP)
				_ = a.AP.Disconnect()
				return
			}
		}
	}
}

func (a *API) connectAP(c *gin.Context) {
	a.Control.Stop()
	if err := a.AP.Connect(); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	a.Rover.SetAddress("192.168.71.1")
	c.JSON(http.StatusOK, gin.H{"rover": a.Rover.Address()})
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	},
}

func (a *API) websocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	owner := a.nextOwner.Add(1)
	defer a.Control.StopOwner(owner)
	conn.SetReadLimit(1024)
	if err := conn.WriteJSON(gin.H{"type": "hello", "operator_id": owner}); err != nil {
		return
	}
	for {
		var message struct {
			Type      string `json:"type"`
			Direction string `json:"direction"`
			Speed     int    `json:"speed"`
			On        bool   `json:"on"`
			Active    bool   `json:"active"`
		}
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		var commandErr error
		switch message.Type {
		case "drive":
			commandErr = a.Control.Drive(owner, message.Direction)
		case "speed":
			commandErr = a.Control.Speed(owner, message.Speed)
		case "light":
			commandErr = a.Rover.Light(c.Request.Context(), message.On)
		case "stop":
			a.Control.Stop()
		case "supervise":
			if message.Active {
				commandErr = a.Control.Supervise(owner)
			} else {
				a.Control.StopOwner(owner)
			}
		default:
			commandErr = errors.New("unknown command")
		}
		if commandErr != nil {
			if err := conn.WriteJSON(gin.H{"type": "error", "message": commandErr.Error()}); err != nil {
				return
			}
		} else if err := conn.WriteJSON(gin.H{"type": "ack", "command": message.Type}); err != nil {
			return
		}
	}
}
