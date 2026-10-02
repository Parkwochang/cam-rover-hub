package config

import "os"

type Config struct {
	ListenAddr       string
	RoverAddr        string
	RoverToken       string
	DBPath           string
	APInterface      string
	APProfile        string
	VisionWorker     string
	CameraConfig     string
	Vocabulary       string
	MapDir           string
	AutoEnabled      bool
	RequireTailscale bool
	AllowedLogin     string
}

func Load() Config {
	return Config{
		ListenAddr:       env("LISTEN_ADDR", "127.0.0.1:8080"),
		RoverAddr:        env("ROVER_ADDR", "cam-rover.local"),
		RoverToken:       os.Getenv("ROVER_API_TOKEN"),
		DBPath:           env("DB_PATH", "data/rover.db"),
		APInterface:      env("AP_INTERFACE", "wlan0"),
		APProfile:        env("AP_PROFILE", "cam-rover"),
		VisionWorker:     env("VISION_WORKER", "vision/build/rover-vision"),
		CameraConfig:     env("CAMERA_CONFIG", "config/camera.yaml"),
		Vocabulary:       env("SLAM_VOCABULARY", "config/orb_vocab.fbow"),
		MapDir:           env("MAP_DIR", "data/maps"),
		AutoEnabled:      os.Getenv("AUTO_ENABLED") == "1",
		RequireTailscale: os.Getenv("REQUIRE_TAILSCALE_IDENTITY") == "1",
		AllowedLogin:     os.Getenv("TAILSCALE_ALLOWED_LOGIN"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
