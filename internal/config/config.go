// Package config loads runtime config from env with sane defaults. One binary, two
// surfaces on two ports (§00 axiom 3); auth is out of scope so there is none here.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	AdminAddr     string // Admin UI surface (:8080)
	ModelAPIAddr  string // Model API surface (:8081)
	MetricsAddr   string // health + metrics (ops)
	DBEngine      string // sqlite | postgres | memory
	DBPath        string // sqlite file path / postgres DSN (ignored for memory)
	StorageDriver string // fs | s3 | oci (§05.3)
	StorageRoot   string // fs backend root (dev default)
	S3            S3Config
	OCI           OCIConfig
	GC            GCConfig
	Cache         CacheConfig
	Tracing       TracingConfig
	ActorHeader   string // trusted identity header for audit (§00 axiom 4)
}

// TracingConfig configures OTLP span export (§09.4). Empty endpoint = tracing off, which is
// the default: correlation IDs work without a collector, spans need one.
type TracingConfig struct {
	Endpoint    string  // OTLP/HTTP collector, host:port or http(s):// URL
	ServiceName string  // resource service.name
	SampleRatio float64 // parent-based head sampling, 0..1
}

// CacheConfig configures the resolution cache (§04.4). memory = dev; redis = prod.
type CacheConfig struct {
	Engine        string // memory | redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int
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

// OCIConfig configures the OCI-registry backend (§05.4.2). Credentials come from env/secret
// and never appear in API responses.
type OCIConfig struct {
	Registry   string // host[:port] — ghcr.io, harbor.internal, registry:5000
	Repository string // repository prefix Lineage owns, e.g. "lineage/models"
	Username   string
	Password   string
	PlainHTTP  bool // http instead of https (in-cluster/dev registries)
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
		// Credentials: a robot account or registry token. Left unset, Lineage talks to the
		// registry anonymously, which is enough for a public pull-only repository (§05.4.2).
		OCI: OCIConfig{
			Registry:   env("LINEAGE_OCI_REGISTRY", ""),
			Repository: env("LINEAGE_OCI_REPOSITORY", ""),
			Username:   env("LINEAGE_OCI_USERNAME", ""),
			Password:   env("LINEAGE_OCI_PASSWORD", ""),
			PlainHTTP:  env("LINEAGE_OCI_PLAIN_HTTP", "") == "true",
		},
		GC: GCConfig{
			Mode:     env("LINEAGE_STORAGE_GC", "retain"),
			Prefix:   env("LINEAGE_GC_PREFIX", ""),
			Grace:    envDuration("LINEAGE_GC_GRACE", 24*time.Hour),
			Interval: envDuration("LINEAGE_GC_INTERVAL", time.Hour),
		},
		Cache: CacheConfig{
			Engine:        env("LINEAGE_CACHE_ENGINE", "memory"),
			RedisAddr:     env("LINEAGE_REDIS_ADDR", "localhost:6379"),
			RedisPassword: env("LINEAGE_REDIS_PASSWORD", ""),
			RedisDB:       envInt("LINEAGE_REDIS_DB", 0),
		},
		Tracing: TracingConfig{
			// OTEL_EXPORTER_OTLP_ENDPOINT is the OTel-standard variable; honour it as a fallback
			// so a collector injected by a sidecar/operator works with no Lineage-specific config.
			Endpoint:    env("LINEAGE_OTLP_ENDPOINT", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
			ServiceName: env("LINEAGE_SERVICE_NAME", "lineage"),
			SampleRatio: envFloat("LINEAGE_TRACE_SAMPLE_RATIO", 1.0),
		},
		ActorHeader: env("LINEAGE_ACTOR_HEADER", "X-Lineage-Actor"),
	}
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
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
