package config

import (
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                   string
	RedisAddr              string
	RedisPass              string
	ScyllaHosts            []string
	ScyllaKeyspace         string
	AstraBundlePath        string
	AstraClientID          string
	AstraClientSecret      string
	AstraAPIURL            string
	AstraDatabaseID        string
	AstraToken             string
	R2AccountId            string
	R2AccessKey            string
	R2SecretKey            string
	R2Bucket               string
	R2PublicBaseURL        string
	MaxDailyRequests       int64
	MaxDailyUploadBytes    int64
	MaxDailyBandwidthBytes int64
	MaxDailyMessages       int64
	MaxDailyWsConnections  int64
	MaxDailyFilesUploaded  int64
}

func LoadConfig() *Config {
	if err := godotenv.Load(".env", "../.env"); err != nil {
		log.Println("No .env file found, using system env variables")
	}

	r2EndpointURL := getAnyEnv("", "R2_S3_endpoint_url", "R2_S3_ENDPOINT_URL")
	r2AccountID := getAnyEnv("", "R2_ACCOUNT_ID")
	if r2AccountID == "" {
		r2AccountID = accountIDFromEndpoint(r2EndpointURL)
	}

	astraAPIURL := getAnyEnv("", "ASTRA_API_URL", "ASTRA_DB_ENDPOINT", "astra_db_endpoint")
	astraDatabaseID := getAnyEnv("", "ASTRA_DB_ID", "ASTRA_DATABASE_ID", "astra_db_id")
	if astraDatabaseID == "" {
		astraDatabaseID = databaseIDFromAstraEndpoint(astraAPIURL)
	}

	return &Config{
		Port:                   getEnv("PORT", "8080"),
		RedisAddr:              getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPass:              getEnv("REDIS_PASS", ""),
		ScyllaHosts:            parseCSVEnv("SCYLLA_HOSTS", "127.0.0.1"),
		ScyllaKeyspace:         getEnv("SCYLLA_KEYSPACE", "converse"),
		AstraBundlePath:        getAnyEnv("", "ASTRA_BUNDLE_PATH"),
		AstraClientID:          getAnyEnv("", "ASTRA_CLIENT_ID"),
		AstraClientSecret:      getAnyEnv("", "ASTRA_CLIENT_SECRET"),
		AstraAPIURL:            astraAPIURL,
		AstraDatabaseID:        astraDatabaseID,
		AstraToken:             getAnyEnv("", "ASTRA_TOKEN", "ASTRA_DB_TOKEN", "astra_db_token", "ASTRA_DB_APP_TOKEN", "astra_db_app_token"),
		R2AccountId:            r2AccountID,
		R2AccessKey:            getAnyEnv("", "R2_ACCESS_KEY", "R2_S3_access_key_id", "R2_S3_ACCESS_KEY_ID"),
		R2SecretKey:            getAnyEnv("", "R2_SECRET_KEY", "R2_S3_secret_access_key", "R2_S3_SECRET_ACCESS_KEY"),
		R2Bucket:               getAnyEnv("", "R2_BUCKET", "R2_S3_bucket_name", "R2_S3_BUCKET_NAME"),
		R2PublicBaseURL:        getAnyEnv("", "R2_PUBLIC_BASE_URL", "R2_S3_PUBLIC_URL"),
		MaxDailyRequests:       getInt64Env("MAX_DAILY_REQUESTS", 50000),
		MaxDailyUploadBytes:    getInt64Env("MAX_DAILY_UPLOAD_BYTES", 2*1024*1024*1024),
		MaxDailyBandwidthBytes: getInt64Env("MAX_DAILY_BANDWIDTH_BYTES", 5*1024*1024*1024),
		MaxDailyMessages:       getInt64Env("MAX_DAILY_MESSAGES", 200000),
		MaxDailyWsConnections:  getInt64Env("MAX_DAILY_WS_CONNECTIONS", 15000),
		MaxDailyFilesUploaded:  getInt64Env("MAX_DAILY_FILES_UPLOADED", 3000),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getInt64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func getAnyEnv(fallback string, keys ...string) string {
	for _, key := range keys {
		if value, exists := os.LookupEnv(key); exists {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return fallback
}

func parseCSVEnv(key, fallback string) []string {
	value := getEnv(key, fallback)
	parts := strings.Split(value, ",")
	hosts := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	if len(hosts) == 0 {
		return []string{"127.0.0.1"}
	}
	return hosts
}

func accountIDFromEndpoint(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}

	host := parsed.Hostname()
	if host == "" {
		return ""
	}

	prefix, _, found := strings.Cut(host, ".r2.cloudflarestorage.com")
	if !found {
		return ""
	}
	return strings.TrimSpace(prefix)
}

func databaseIDFromAstraEndpoint(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}

	host := parsed.Hostname()
	if host == "" {
		return ""
	}

	if len(host) < 36 {
		return ""
	}
	candidate := host[:36]
	if !isUUIDLike(candidate) {
		return ""
	}
	return candidate
}

func isUUIDLike(input string) bool {
	if len(input) != 36 {
		return false
	}
	for index, ch := range input {
		switch index {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
				return false
			}
		}
	}
	return true
}
