package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/rover"
	"github.com/Parkwochang/cam-rover-hub/internal/store"
	"github.com/Parkwochang/cam-rover-hub/internal/vision"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type APConnector interface {
	Connect() error
	Disconnect() error
}

type API struct {
	Rover       *rover.Client
	Control     *control.Coordinator
	AP          APConnector
	Maps        *vision.Manager
	AutoEnabled bool
	Video       VideoLink
	SettingsDB  *sql.DB
	nextOwner   atomic.Uint64
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
	router.GET("/api/link", a.link)
	router.PUT("/api/link", a.setLink)
	router.POST("/api/link/reconnect", a.reconnect)
	router.GET("/api/maps", a.listMaps)
	router.POST("/api/maps", a.createMap)
	router.GET("/api/maps/status", a.mapStatus)
	router.GET("/api/maps/:id/poses", a.mapPoses)
	router.POST("/api/maps/:id/load", a.loadMap)
	router.POST("/api/maps/save", a.saveMap)
}

func (a *API) status(c *gin.Context) {
	status := a.Control.Status()
	c.JSON(http.StatusOK, gin.H{"mode": status.Mode, "direction": status.Direction, "occupied": status.Occupied, "fault": status.Fault, "rover": a.Rover.Address(), "auto_enabled": a.AutoEnabled})
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
	if input.Mode == "auto" && a.Maps != nil {
		if !a.Control.IsOperator(input.OperatorID) {
			c.JSON(http.StatusConflict, gin.H{"error": "active operator required"})
			return
		}
		saved, err := store.LatestSavedMap(c.Request.Context(), a.Maps.DB())
		if errors.Is(err, sql.ErrNoRows) {
			if current := a.Maps.Status(); current.Running {
				c.JSON(http.StatusAccepted, gin.H{"mode": "manual", "mapping": true, "map_id": current.MapID, "message": "지도 작업이 진행 중입니다."})
				return
			}
			a.Control.Stop()
			m, startErr := a.Maps.StartNew(c.Request.Context(), "자동 탐색 준비 지도")
			if startErr != nil {
				c.JSON(http.StatusConflict, gin.H{"error": startErr.Error()})
				return
			}
			c.JSON(http.StatusAccepted, gin.H{"mode": "manual", "mapping": true, "map_id": m.ID, "message": "지도 작업을 시작했습니다. 감독하에 수동으로 천천히 이동하며 지도 저장 후 다시 시도하세요."})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "map lookup failed"})
			return
		}
		if !a.Maps.Status().Running {
			a.Control.Stop()
			if err := a.Maps.Load(c.Request.Context(), saved.ID); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusAccepted, gin.H{"mode": "manual", "relocalizing": true, "map_id": saved.ID, "message": "저장된 지도를 불러왔습니다. 위치 재인식 전에는 주행할 수 없습니다."})
			return
		}
		if !a.AutoEnabled {
			c.JSON(http.StatusConflict, gin.H{"error": "automatic motion is disabled until Pi/rover acceptance checks pass"})
			return
		}
		if !a.Maps.ReadyForAuto() {
			c.JSON(http.StatusConflict, gin.H{"error": "map relocalization or visual clearance is unavailable; rover remains stopped"})
			return
		}
	}
	if err := a.Control.SetMode(input.OperatorID, input.Mode); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, a.Control.Status())
}

func (a *API) listMaps(c *gin.Context) {
	maps, err := store.ListMaps(c.Request.Context(), a.Maps.DB())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "maps unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"maps": maps})
}

func (a *API) createMap(c *gin.Context) {
	var input struct {
		Name       string `json:"name"`
		OperatorID uint64 `json:"operator_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256)
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid map request"})
		return
	}
	if !a.Control.IsOperator(input.OperatorID) {
		c.JSON(http.StatusConflict, gin.H{"error": "active operator required"})
		return
	}
	a.Control.Stop()
	m, err := a.Maps.StartNew(c.Request.Context(), input.Name)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, m)
}

func (a *API) mapStatus(c *gin.Context) { c.JSON(http.StatusOK, a.Maps.Status()) }

func (a *API) mapPoses(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid map id"})
		return
	}
	poses, err := store.ListPoses(c.Request.Context(), a.Maps.DB(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "poses unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"poses": poses})
}

func (a *API) loadMap(c *gin.Context) {
	var input struct {
		OperatorID uint64 `json:"operator_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128)
	if err := c.ShouldBindJSON(&input); err != nil || !a.Control.IsOperator(input.OperatorID) {
		c.JSON(http.StatusConflict, gin.H{"error": "active operator required"})
		return
	}
	a.Control.Stop()
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid map id"})
		return
	}
	if err := a.Maps.Load(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, a.Maps.Status())
}

func (a *API) saveMap(c *gin.Context) {
	var input struct {
		OperatorID uint64 `json:"operator_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128)
	if err := c.ShouldBindJSON(&input); err != nil || !a.Control.IsOperator(input.OperatorID) {
		c.JSON(http.StatusConflict, gin.H{"error": "active operator required"})
		return
	}
	a.Control.Stop()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := a.Maps.StopAndSave(ctx); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
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
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(gin.H{"type": "hello", "operator_id": owner}); err != nil {
		return
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var message struct {
			RequestID uint64 `json:"request_id"`
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
		case "activate":
			commandErr = a.Control.ActivateManual(owner, message.Speed)
		case "drive":
			commandErr = a.Control.Drive(owner, message.Direction)
		case "speed":
			commandErr = a.Control.Speed(owner, message.Speed)
		case "light":
			commandErr = a.Rover.Light(c.Request.Context(), message.On)
		case "stop":
			commandErr = a.Control.Stop()
		case "supervise":
			if message.Active {
				commandErr = a.Control.Supervise(owner)
			} else {
				a.Control.StopOwner(owner)
			}
		default:
			commandErr = errors.New("unknown command")
		}
		// Start the write budget after the bounded rover operation finishes.
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if commandErr != nil {
			if err := conn.WriteJSON(gin.H{"type": "error", "command": message.Type, "request_id": message.RequestID, "message": commandErr.Error()}); err != nil {
				return
			}
		} else if err := conn.WriteJSON(gin.H{"type": "ack", "command": message.Type, "request_id": message.RequestID, "status": a.Control.Status()}); err != nil {
			return
		}
	}
}
