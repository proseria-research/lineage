// Package config loads runtime config from env with sane defaults. One binary, two
// surfaces on two ports (§00 axiom 3); auth is out of scope so there is none here.
package config

import "os"

type Config struct {
	AdminAddr    string // Admin UI surface (:8080)
	ModelAPIAddr string // Model API surface (:8081)
	MetricsAddr  string // health + metrics (ops)
	DBEngine     string // sqlite | postgres | memory
	DBPath       string // sqlite file path / postgres DSN (ignored for memory)
	StorageRoot  string // fs backend root (dev default)
	ActorHeader  string // trusted identity header for audit (§00 axiom 4)
}

func Load() Config {
	return Config{
		AdminAddr:    env("LINEAGE_ADMIN_ADDR", ":8080"),
		ModelAPIAddr: env("LINEAGE_MODEL_API_ADDR", ":8081"),
		MetricsAddr:  env("LINEAGE_METRICS_ADDR", ":9090"),
		DBEngine:     env("LINEAGE_DB_ENGINE", "sqlite"),
		DBPath:       env("LINEAGE_DB_PATH", "lineage.db"),
		StorageRoot:  env("LINEAGE_STORAGE_ROOT", "./data/artifacts"),
		ActorHeader:  env("LINEAGE_ACTOR_HEADER", "X-Lineage-Actor"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
