package config

import (
	"log"
	"net"
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
	AppSecretKey           string
	TrustedProxies         []string
	ScyllaHosts            []string
	ScyllaKeyspace         string
	AstraBundlePath        string
	AstraAPIURL            string
	AstraDatabaseID        string
	AstraRegion            string
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
	if err := godotenv.Load("../.env", ".env"); 
	err != nil {
		log.Println("No .env file found, using system env variables")
	}
	appSecretKey := requireAppSecretKey()

	r2EndpointURL := getAnyEnv("", "R2_S3_endpoint_url", "R2_S3_ENDPOINT_URL")
	r2AccountID := getAnyEnv("", "R2_ACCOUNT_ID")
	if r2AccountID == "" {
		r2AccountID = accountIDFromEndpoint(r2EndpointURL)
	}

	rawAstraEndpoint := getAnyEnv("", "ASTRA_API_URL", "ASTRA_DB_ENDPOINT", "astra_db_endpoint")
	astraAPIURL := normalizeAstraAPIURL(rawAstraEndpoint)
	if astraAPIURL == "" {
		astraAPIURL = "https://api.astra.datastax.com"
	}
	astraDatabaseID := getAnyEnv("", "ASTRA_DB_ID")
	if astraDatabaseID == "" {
		astraDatabaseID = databaseIDFromAstraEndpoint(rawAstraEndpoint)
	}
	astraRegion := getAnyEnv("", "ASTRA_DB_REGION", "astra_db_region")
	if astraRegion == "" {
		astraRegion = regionFromAstraEndpoint(rawAstraEndpoint)
	}

	return &Config{
		Port:                   getEnv("PORT", "8080"),
		RedisAddr:              normalizeRedisAddr(getEnv("REDIS_ADDR", "127.0.0.1:6379")),
		RedisPass:              getEnv("REDIS_PASS", ""),
		AppSecretKey:           appSecretKey,
		TrustedProxies:         parseCSVEnvOptional("TRUSTED_PROXIES"),
		ScyllaHosts:            parseCSVEnv("SCYLLA_HOSTS", "127.0.0.1"),
		ScyllaKeyspace:         getAnyEnv("converse", "SCYLLA_KEYSPACE", "KEYSPACE_NAME"),
		AstraBundlePath:        getAnyEnv("", "ASTRA_BUNDLE_PATH"),
		AstraAPIURL:            astraAPIURL,
		AstraDatabaseID:        astraDatabaseID,
		AstraRegion:            astraRegion,
		AstraToken:             getAnyEnv("", "ASTRA_TOKEN", "APPLICATION_TOKEN"),
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

func normalizeRedisAddr(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return "127.0.0.1:6379"
	}

	host, port, err := net.SplitHostPort(normalized)
	if err == nil {
		if strings.EqualFold(strings.TrimSpace(host), "localhost") {
			return net.JoinHostPort("127.0.0.1", strings.TrimSpace(port))
		}
		return normalized
	}

	// Handle values without an explicit port.
	if strings.EqualFold(normalized, "localhost") {
		return "127.0.0.1:6379"
	}

	return normalized
}

func requireAppSecretKey() string {
	key, exists := os.LookupEnv("APP_SECRET_KEY")
	if !exists || key == "" {
		log.Fatal("APP_SECRET_KEY must be set and exactly 32 characters long")
	}
	if len(key) != 32 {
		log.Fatalf("APP_SECRET_KEY must be exactly 32 characters long (got %d)", len(key))
	}
	return key
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
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

func parseCSVEnvOptional(key string) []string {
	rawValue, exists := os.LookupEnv(key)
	if !exists {
		return []string{}
	}
	value := strings.TrimSpace(rawValue)
	if value == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
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

func normalizeAstraAPIURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return trimmed
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return trimmed
	}

	// `*.apps.astra.datastax.com` is a DB endpoint, not the DevOps API base.
	if strings.HasSuffix(host, ".apps.astra.datastax.com") || host == "apps.astra.datastax.com" {
		return "https://api.astra.datastax.com"
	}
	if host == "api.astra.datastax.com" {
		return "https://api.astra.datastax.com"
	}

	scheme := parsed.Scheme
	if strings.TrimSpace(scheme) == "" {
		scheme = "https"
	}
	return scheme + "://" + parsed.Host
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

func regionFromAstraEndpoint(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}

	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return ""
	}

	parts := strings.Split(host, ".")
	if len(parts) < 4 {
		return ""
	}
	if parts[len(parts)-4] != "apps" || parts[len(parts)-3] != "astra" || parts[len(parts)-2] != "datastax" || parts[len(parts)-1] != "com" {
		return ""
	}

	prefix := parts[0]
	if len(prefix) <= 37 {
		return ""
	}
	region := strings.TrimSpace(prefix[37:])
	if region == "" {
		return ""
	}
	return region
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
