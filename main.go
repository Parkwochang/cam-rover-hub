package main

import (
	"log"
	"net/http"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/api"
	"github.com/Parkwochang/cam-rover-hub/internal/config"
	"github.com/Parkwochang/cam-rover-hub/internal/control"
	"github.com/Parkwochang/cam-rover-hub/internal/network"
	"github.com/Parkwochang/cam-rover-hub/internal/rover"
	"github.com/Parkwochang/cam-rover-hub/internal/store"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	roverClient := rover.New(cfg.RoverAddr, cfg.RoverToken)
	coordinator := control.New(roverClient)
	defer coordinator.Close()
	hubAPI := &api.API{
		Rover:   roverClient,
		Control: coordinator,
		AP:      network.APConnector{Profile: cfg.APProfile, Interface: cfg.APInterface},
	}

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	hubAPI.Register(router)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("cam-rover-hub listening on %s", cfg.ListenAddr)
	log.Fatal(server.ListenAndServe())
}
