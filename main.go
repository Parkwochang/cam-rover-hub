package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/api"
	"github.com/Parkwochang/cam-rover-hub/internal/autonomy"
	"github.com/Parkwochang/cam-rover-hub/internal/config"
	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/network"
	"github.com/Parkwochang/cam-rover-hub/internal/rover"
	"github.com/Parkwochang/cam-rover-hub/internal/security"
	"github.com/Parkwochang/cam-rover-hub/internal/store"
	"github.com/Parkwochang/cam-rover-hub/internal/video"
	"github.com/Parkwochang/cam-rover-hub/internal/vision"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	if err := security.ValidateBind(cfg.ListenAddr, cfg.RequireTailscale, cfg.AllowedLogin); err != nil {
		log.Fatal(err)
	}
	if cfg.RequireTailscale && (cfg.AllowedLogin == "owner@example.com" || cfg.RoverToken == "" || strings.HasPrefix(cfg.RoverToken, "REPLACE_")) {
		log.Fatal("set a real TAILSCALE_ALLOWED_LOGIN and ROVER_API_TOKEN before deployment")
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	settingsCtx, settingsCancel := context.WithTimeout(context.Background(), 2*time.Second)
	savedAddress, err := store.RoverAddress(settingsCtx, db)
	settingsCancel()
	if err != nil {
		log.Fatal("could not load rover address settings")
	}
	if savedAddress != "" {
		cfg.RoverAddr = savedAddress
	}
	address, err := rover.NormalizeAddress(cfg.RoverAddr)
	if err != nil {
		log.Fatal("invalid ROVER_ADDR; use a private LAN IP or .local hostname")
	}
	roverClient := rover.New(address, cfg.RoverToken)
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	broker := video.New(roverClient)
	go broker.Run(streamCtx)
	coordinator := control.New(roverClient)
	defer coordinator.Close()
	maps := vision.New(db, broker, vision.Config{Worker: cfg.VisionWorker, Camera: cfg.CameraConfig, Vocabulary: cfg.Vocabulary, MapDir: cfg.MapDir})
	defer maps.Close()
	coordinator.SetGuards(broker.Healthy, func() bool { return cfg.AutoEnabled && maps.ReadyForAuto() })
	if cfg.AutoEnabled {
		go autonomy.Run(streamCtx, coordinator, maps)
	}
	hubAPI := &api.API{
		Rover:       roverClient,
		Control:     coordinator,
		AP:          network.APConnector{Profile: cfg.APProfile, Interface: cfg.APInterface},
		Maps:        maps,
		AutoEnabled: cfg.AutoEnabled,
		Video:       broker,
		SettingsDB:  db,
	}

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.Use(security.RequireIdentity(cfg.RequireTailscale, cfg.AllowedLogin))
	hubAPI.Register(router)
	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})
	router.GET("/video.mjpeg", gin.WrapH(broker))

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("cam-rover-hub listening on %s", cfg.ListenAddr)
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	select {
	case <-shutdownCtx.Done():
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("server stopped: %v", err)
		}
	}
	coordinator.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
}
