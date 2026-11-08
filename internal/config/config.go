package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	ListenAddr         string
	DataDir            string
	DatabasePath       string
	UploadDir          string
	AdminUser          string
	AdminPassword      string
	MaxUploadBytes     int64
	MaxFilesPerRequest int
	RetentionHours     int
}

func Load() Config {
	data := env("CAFEDROP_DATA_DIR", "./data")
	maxBytes := int64(envInt("CAFEDROP_MAX_UPLOAD_MB", 200)) * 1024 * 1024
	return Config{
		ListenAddr:         env("CAFEDROP_LISTEN", "0.0.0.0:8080"),
		DataDir:            data,
		DatabasePath:       filepath.Join(data, "cafedrop.db"),
		UploadDir:          filepath.Join(data, "uploads"),
		AdminUser:          env("CAFEDROP_ADMIN_USER", "admin"),
		AdminPassword:      env("CAFEDROP_ADMIN_PASSWORD", "change-this-password"),
		MaxUploadBytes:     maxBytes,
		MaxFilesPerRequest: envInt("CAFEDROP_MAX_FILES", 500),
		RetentionHours:     envInt("CAFEDROP_RETENTION_HOURS", 24),
	}
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func envInt(k string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(k))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
