package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Parkwochang/cam-rover-hub/internal/rover"
	"github.com/Parkwochang/cam-rover-hub/internal/store"
	"github.com/Parkwochang/cam-rover-hub/internal/video"
	"github.com/gin-gonic/gin"
)

type VideoLink interface {
	Status() video.Status
	Reconnect()
}

func (a *API) link(c *gin.Context) {
	result := gin.H{"address": a.Rover.Address()}
	if a.Video != nil {
		result["video"] = a.Video.Status()
	}
	c.JSON(http.StatusOK, result)
}

func (a *API) setLink(c *gin.Context) {
	var input struct {
		Address string `json:"address"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256)
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid address request"})
		return
	}
	address, err := rover.NormalizeAddress(input.Address)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	err = a.Control.Reconfigure(func() error {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if a.SettingsDB != nil {
			if err := store.SaveRoverAddress(ctx, a.SettingsDB, address); err != nil {
				return err
			}
		}
		a.Rover.SetAddress(address)
		if a.Video != nil {
			a.Video.Reconnect()
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "address could not be saved; rover remains stopped"})
		return
	}
	a.link(c)
}

func (a *API) reconnect(c *gin.Context) {
	if err := a.Control.Reconfigure(func() error {
		if a.Video != nil {
			a.Video.Reconnect()
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "another transition is in progress"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"reconnecting": true, "direction": "stop"})
}
