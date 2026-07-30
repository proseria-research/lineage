// Package config loads runtime config from env with sane defaults. One binary, two
// surfaces on two ports (§00 axiom 3); auth is out of scope so there is none here.
package config

import (
	"os"
	"time"
)

type Config struct {
	AdminAddr     string // Admin UI surface (:8080)
	ModelAPIAddr  string // Model API surface (:8081)
	MetricsAddr   string // health + metrics (ops)
	DBEngine      string // sqlite | postgres | memory
	DBPath        string // sqlite file path / postgres DSN (ignored for memory)
	StorageDriver string // fs | s3 (§05.3)
	StorageRoot   string // fs backend root (dev default)
	S3            S3Config
	GC            GCConfig
	ActorHeader   string // trusted identity header for audit (§00 axiom 4)
}

// GCConfig configures artifact garbage collection (§05.8). Default is retain.
type GCConfig struct {
	Mode     string        // retain | sweep
	Prefix   string        // storage prefix Lineage owns (path-scoped safety)
	Grace    time.Duration // min object age before eligible
	Interval time.Duration // sweep period
}

// S3Config configures the S3-compatible backend (§05.4). Credentials come from env/secret
// and never appear in API responses.
type S3Config struct {
	Bucket       string
	Region       string
	Endpoint     string // S3-compatible endpoint override (MinIO/R2/Ceph); empty = AWS
	AccessKey    string
	SecretKey    string
	SessionToken string
	PathStyle    bool
}

func Load() Config {
	return Config{
		AdminAddr:     env("LINEAGE_ADMIN_ADDR", ":8080"),
		ModelAPIAddr:  env("LINEAGE_MODEL_API_ADDR", ":8081"),
		MetricsAddr:   env("LINEAGE_METRICS_ADDR", ":9090"),
		DBEngine:      env("LINEAGE_DB_ENGINE", "sqlite"),
		DBPath:        env("LINEAGE_DB_PATH", "lineage.db"),
		StorageDriver: env("LINEAGE_STORAGE_DRIVER", "fs"),
		StorageRoot:   env("LINEAGE_STORAGE_ROOT", "./data/artifacts"),
		// Credentials: setting LINEAGE_S3_ACCESS_KEY/SECRET_KEY pins static keys. Leave them
		// unset to use the auto chain (env → IRSA/Pod Identity → ECS task role → EC2 IMDS, §05.4).
		S3: S3Config{
			Bucket:       env("LINEAGE_S3_BUCKET", ""),
			Region:       env("LINEAGE_S3_REGION", ""),
			Endpoint:     env("LINEAGE_S3_ENDPOINT", ""),
			AccessKey:    env("LINEAGE_S3_ACCESS_KEY", ""),
			SecretKey:    env("LINEAGE_S3_SECRET_KEY", ""),
			SessionToken: env("LINEAGE_S3_SESSION_TOKEN", ""),
			PathStyle:    env("LINEAGE_S3_PATH_STYLE", "") == "true",
		},
		GC: GCConfig{
			Mode:     env("LINEAGE_STORAGE_GC", "retain"),
			Prefix:   env("LINEAGE_GC_PREFIX", ""),
			Grace:    envDuration("LINEAGE_GC_GRACE", 24*time.Hour),
			Interval: envDuration("LINEAGE_GC_INTERVAL", time.Hour),
		},
		ActorHeader: env("LINEAGE_ACTOR_HEADER", "X-Lineage-Actor"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
