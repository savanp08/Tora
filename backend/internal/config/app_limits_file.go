package config

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const appLimitReloadInterval = 15 * time.Second

type TimeWindowLimit struct {
	PerHour  int64
	PerDay   int64
	PerWeek  int64
	PerMonth int64
}

type AppLimits struct {
	AI     AILimits
	Board  BoardLimits
	Room   RoomLimits
	Upload UploadLimits
	WS     WebSocketLimits
}

type AILimits struct {
	ContextMessageLimit     int
	OrganizeMaxRequestBytes int64
	OrganizeMaxItems        int
	OrganizeNoteMaxLength   int
	OrganizeTopicMaxLength  int
	OrganizeTextMaxLength   int
	OrganizeRequestTimeout  time.Duration
	UserRequestLimits       TimeWindowLimit
	RoomRequestLimits       TimeWindowLimit
	IPRequestLimits         TimeWindowLimit
	DeviceRequestLimits     TimeWindowLimit
}

type BoardLimits struct {
	MaxStorageBytes int64
}

type RoomLimits struct {
	CodeDigits        int
	NameMaxLength     int
	PasswordMaxLength int
	MaxDescendants    int
	MaxDurationHours  float64
}

type UploadRateScopeLimits struct {
	User   TimeWindowLimit
	IP     TimeWindowLimit
	Device TimeWindowLimit
}

type UploadRateLimits struct {
	GenerateURL UploadRateScopeLimits
	Proxy       UploadRateScopeLimits
}

type UploadLimits struct {
	MaxFileBytes       int64
	MaxImageBytes      int64
	MaxMultipartBytes  int64
	MaxFormFieldLength int64
	Rate               UploadRateLimits
}

type WebSocketRateLimits struct {
	ConnectUser   TimeWindowLimit
	ConnectIP     TimeWindowLimit
	ConnectDevice TimeWindowLimit
}

type WebSocketLimits struct {
	MaxMessageSize        int64
	MaxTextChars          int
	MaxMediaURLLength     int
	MaxFileNameLength     int
	MaxGlobalConnections  int32
	MaxConnectionsPerIP   int32
	MaxConnectionsPerRoom int
	Rate                  WebSocketRateLimits
}

var (
	defaultAppLimits = AppLimits{
		AI: AILimits{
			ContextMessageLimit:     50,
			OrganizeMaxRequestBytes: 2 * 1024 * 1024,
			OrganizeMaxItems:        500,
			OrganizeNoteMaxLength:   1200,
			OrganizeTopicMaxLength:  180,
			OrganizeTextMaxLength:   3000,
			OrganizeRequestTimeout:  30 * time.Second,
			UserRequestLimits: TimeWindowLimit{
				PerHour:  24,
				PerDay:   120,
				PerWeek:  600,
				PerMonth: 1800,
			},
			RoomRequestLimits: TimeWindowLimit{
				PerHour:  80,
				PerDay:   500,
				PerWeek:  2500,
				PerMonth: 7000,
			},
			IPRequestLimits: TimeWindowLimit{
				PerHour:  40,
				PerDay:   220,
				PerWeek:  1000,
				PerMonth: 3000,
			},
			DeviceRequestLimits: TimeWindowLimit{
				PerHour:  30,
				PerDay:   180,
				PerWeek:  800,
				PerMonth: 2400,
			},
		},
		Board: BoardLimits{
			MaxStorageBytes: 10 * 1024 * 1024,
		},
		Room: RoomLimits{
			CodeDigits:        6,
			NameMaxLength:     20,
			PasswordMaxLength: 64,
			MaxDescendants:    6,
			MaxDurationHours:  360,
		},
		Upload: UploadLimits{
			MaxFileBytes:       5 * 1024 * 1024,
			MaxImageBytes:      5 * 1024 * 1024,
			MaxMultipartBytes:  6 * 1024 * 1024,
			MaxFormFieldLength: 1024,
			Rate: UploadRateLimits{
				GenerateURL: UploadRateScopeLimits{
					User: TimeWindowLimit{
						PerHour:  25,
						PerDay:   120,
						PerWeek:  500,
						PerMonth: 1500,
					},
					IP: TimeWindowLimit{
						PerHour:  30,
						PerDay:   150,
						PerWeek:  600,
						PerMonth: 1800,
					},
					Device: TimeWindowLimit{
						PerHour:  20,
						PerDay:   100,
						PerWeek:  450,
						PerMonth: 1400,
					},
				},
				Proxy: UploadRateScopeLimits{
					User: TimeWindowLimit{
						PerHour:  15,
						PerDay:   70,
						PerWeek:  300,
						PerMonth: 900,
					},
					IP: TimeWindowLimit{
						PerHour:  20,
						PerDay:   90,
						PerWeek:  350,
						PerMonth: 1000,
					},
					Device: TimeWindowLimit{
						PerHour:  12,
						PerDay:   60,
						PerWeek:  250,
						PerMonth: 750,
					},
				},
			},
		},
		WS: WebSocketLimits{
			MaxMessageSize:        65536,
			MaxTextChars:          4000,
			MaxMediaURLLength:     4096,
			MaxFileNameLength:     180,
			MaxGlobalConnections:  60000,
			MaxConnectionsPerIP:   2000,
			MaxConnectionsPerRoom: 6,
			Rate: WebSocketRateLimits{
				ConnectUser: TimeWindowLimit{
					PerHour:  120,
					PerDay:   1000,
					PerWeek:  4000,
					PerMonth: 12000,
				},
				ConnectIP: TimeWindowLimit{
					PerHour:  180,
					PerDay:   1200,
					PerWeek:  5000,
					PerMonth: 15000,
				},
				ConnectDevice: TimeWindowLimit{
					PerHour:  120,
					PerDay:   900,
					PerWeek:  3600,
					PerMonth: 10000,
				},
			},
		},
	}

	appLimitPatternCache sync.Map

	appLimitsState struct {
		mu           sync.Mutex
		lastLoadedAt time.Time
		values       AppLimits
	}
)

func LoadAppLimits() AppLimits {
	appLimitsState.mu.Lock()
	defer appLimitsState.mu.Unlock()

	now := time.Now().UTC()
	if !appLimitsState.lastLoadedAt.IsZero() && now.Sub(appLimitsState.lastLoadedAt) < appLimitReloadInterval {
		return appLimitsState.values.normalized()
	}

	path := resolveAppLimitsFilePath()
	loaded := readAppLimitsFromFile(path)
	appLimitsState.values = loaded.normalized()
	appLimitsState.lastLoadedAt = now
	return appLimitsState.values
}

func resolveAppLimitsFilePath() string {
	candidates := make([]string, 0, 6)
	if configured := strings.TrimSpace(os.Getenv("APP_LIMITS_FILE")); configured != "" {
		candidates = append(candidates, configured)
	}
	if configured := strings.TrimSpace(os.Getenv("AI_LIMITS_FILE")); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates,
		"limits.ts",
		filepath.Join("..", "limits.ts"),
		filepath.Join("..", "..", "limits.ts"),
	)

	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate
	}
	return ""
}

func readAppLimitsFromFile(path string) AppLimits {
	limits := defaultAppLimits
	if strings.TrimSpace(path) == "" {
		return limits
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[limits] failed to read limits.ts path=%s err=%v; using defaults", path, err)
		return limits
	}
	content := string(data)

	limits.Board.MaxStorageBytes = parseAppLimitInt64(content, "maxStorageBytes", limits.Board.MaxStorageBytes)
	limits.AI.ContextMessageLimit = int(parseAppLimitInt64(content, "contextMessageLimit", int64(limits.AI.ContextMessageLimit)))
	limits.AI.OrganizeMaxRequestBytes = parseAppLimitInt64(content, "organizeMaxRequestBytes", limits.AI.OrganizeMaxRequestBytes)
	limits.AI.OrganizeMaxItems = int(parseAppLimitInt64(content, "organizeMaxItems", int64(limits.AI.OrganizeMaxItems)))
	limits.AI.OrganizeNoteMaxLength = int(parseAppLimitInt64(content, "organizeNoteMaxLength", int64(limits.AI.OrganizeNoteMaxLength)))
	limits.AI.OrganizeTopicMaxLength = int(parseAppLimitInt64(content, "organizeTopicMaxLength", int64(limits.AI.OrganizeTopicMaxLength)))
	limits.AI.OrganizeTextMaxLength = int(parseAppLimitInt64(content, "organizeTextMaxLength", int64(limits.AI.OrganizeTextMaxLength)))
	limits.AI.OrganizeRequestTimeout = time.Duration(parseAppLimitInt64(content, "organizeRequestTimeoutMs", int64(limits.AI.OrganizeRequestTimeout/time.Millisecond))) * time.Millisecond

	limits.AI.UserRequestLimits.PerHour = parseAppLimitInt64(content, "privateUserPerHour", limits.AI.UserRequestLimits.PerHour)
	limits.AI.UserRequestLimits.PerDay = parseAppLimitInt64(content, "privateUserPerDay", limits.AI.UserRequestLimits.PerDay)
	limits.AI.UserRequestLimits.PerWeek = parseAppLimitInt64(content, "privateUserPerWeek", limits.AI.UserRequestLimits.PerWeek)
	limits.AI.UserRequestLimits.PerMonth = parseAppLimitInt64(content, "privateUserPerMonth", limits.AI.UserRequestLimits.PerMonth)

	limits.AI.RoomRequestLimits.PerHour = parseAppLimitInt64(content, "privateRoomPerHour", limits.AI.RoomRequestLimits.PerHour)
	limits.AI.RoomRequestLimits.PerDay = parseAppLimitInt64(content, "privateRoomPerDay", limits.AI.RoomRequestLimits.PerDay)
	limits.AI.RoomRequestLimits.PerWeek = parseAppLimitInt64(content, "privateRoomPerWeek", limits.AI.RoomRequestLimits.PerWeek)
	limits.AI.RoomRequestLimits.PerMonth = parseAppLimitInt64(content, "privateRoomPerMonth", limits.AI.RoomRequestLimits.PerMonth)

	limits.AI.IPRequestLimits.PerHour = parseAppLimitInt64(content, "privateIpPerHour", limits.AI.IPRequestLimits.PerHour)
	limits.AI.IPRequestLimits.PerDay = parseAppLimitInt64(content, "privateIpPerDay", limits.AI.IPRequestLimits.PerDay)
	limits.AI.IPRequestLimits.PerWeek = parseAppLimitInt64(content, "privateIpPerWeek", limits.AI.IPRequestLimits.PerWeek)
	limits.AI.IPRequestLimits.PerMonth = parseAppLimitInt64(content, "privateIpPerMonth", limits.AI.IPRequestLimits.PerMonth)

	limits.AI.DeviceRequestLimits.PerHour = parseAppLimitInt64(content, "privateDevicePerHour", limits.AI.DeviceRequestLimits.PerHour)
	limits.AI.DeviceRequestLimits.PerDay = parseAppLimitInt64(content, "privateDevicePerDay", limits.AI.DeviceRequestLimits.PerDay)
	limits.AI.DeviceRequestLimits.PerWeek = parseAppLimitInt64(content, "privateDevicePerWeek", limits.AI.DeviceRequestLimits.PerWeek)
	limits.AI.DeviceRequestLimits.PerMonth = parseAppLimitInt64(content, "privateDevicePerMonth", limits.AI.DeviceRequestLimits.PerMonth)

	limits.Room.CodeDigits = int(parseAppLimitInt64(content, "codeDigits", int64(limits.Room.CodeDigits)))
	limits.Room.NameMaxLength = int(parseAppLimitInt64(content, "nameMaxLength", int64(limits.Room.NameMaxLength)))
	limits.Room.PasswordMaxLength = int(parseAppLimitInt64(content, "passwordMaxLength", int64(limits.Room.PasswordMaxLength)))
	limits.Room.MaxDescendants = int(parseAppLimitInt64(content, "maxDescendants", int64(limits.Room.MaxDescendants)))
	limits.Room.MaxDurationHours = parseAppLimitFloat64(content, "maxDurationHours", limits.Room.MaxDurationHours)

	limits.Upload.MaxFileBytes = parseAppLimitInt64(content, "maxFileBytes", limits.Upload.MaxFileBytes)
	limits.Upload.MaxImageBytes = parseAppLimitInt64(content, "maxImageBytes", limits.Upload.MaxImageBytes)
	limits.Upload.MaxMultipartBytes = parseAppLimitInt64(content, "maxMultipartBytes", limits.Upload.MaxMultipartBytes)
	limits.Upload.MaxFormFieldLength = parseAppLimitInt64(content, "maxFormFieldLength", limits.Upload.MaxFormFieldLength)

	limits.Upload.Rate.GenerateURL.User.PerHour = parseAppLimitInt64(content, "generateUrlUserPerHour", limits.Upload.Rate.GenerateURL.User.PerHour)
	limits.Upload.Rate.GenerateURL.User.PerDay = parseAppLimitInt64(content, "generateUrlUserPerDay", limits.Upload.Rate.GenerateURL.User.PerDay)
	limits.Upload.Rate.GenerateURL.User.PerWeek = parseAppLimitInt64(content, "generateUrlUserPerWeek", limits.Upload.Rate.GenerateURL.User.PerWeek)
	limits.Upload.Rate.GenerateURL.User.PerMonth = parseAppLimitInt64(content, "generateUrlUserPerMonth", limits.Upload.Rate.GenerateURL.User.PerMonth)
	limits.Upload.Rate.GenerateURL.IP.PerHour = parseAppLimitInt64(content, "generateUrlIpPerHour", limits.Upload.Rate.GenerateURL.IP.PerHour)
	limits.Upload.Rate.GenerateURL.IP.PerDay = parseAppLimitInt64(content, "generateUrlIpPerDay", limits.Upload.Rate.GenerateURL.IP.PerDay)
	limits.Upload.Rate.GenerateURL.IP.PerWeek = parseAppLimitInt64(content, "generateUrlIpPerWeek", limits.Upload.Rate.GenerateURL.IP.PerWeek)
	limits.Upload.Rate.GenerateURL.IP.PerMonth = parseAppLimitInt64(content, "generateUrlIpPerMonth", limits.Upload.Rate.GenerateURL.IP.PerMonth)
	limits.Upload.Rate.GenerateURL.Device.PerHour = parseAppLimitInt64(content, "generateUrlDevicePerHour", limits.Upload.Rate.GenerateURL.Device.PerHour)
	limits.Upload.Rate.GenerateURL.Device.PerDay = parseAppLimitInt64(content, "generateUrlDevicePerDay", limits.Upload.Rate.GenerateURL.Device.PerDay)
	limits.Upload.Rate.GenerateURL.Device.PerWeek = parseAppLimitInt64(content, "generateUrlDevicePerWeek", limits.Upload.Rate.GenerateURL.Device.PerWeek)
	limits.Upload.Rate.GenerateURL.Device.PerMonth = parseAppLimitInt64(content, "generateUrlDevicePerMonth", limits.Upload.Rate.GenerateURL.Device.PerMonth)

	limits.Upload.Rate.Proxy.User.PerHour = parseAppLimitInt64(content, "proxyUserPerHour", limits.Upload.Rate.Proxy.User.PerHour)
	limits.Upload.Rate.Proxy.User.PerDay = parseAppLimitInt64(content, "proxyUserPerDay", limits.Upload.Rate.Proxy.User.PerDay)
	limits.Upload.Rate.Proxy.User.PerWeek = parseAppLimitInt64(content, "proxyUserPerWeek", limits.Upload.Rate.Proxy.User.PerWeek)
	limits.Upload.Rate.Proxy.User.PerMonth = parseAppLimitInt64(content, "proxyUserPerMonth", limits.Upload.Rate.Proxy.User.PerMonth)
	limits.Upload.Rate.Proxy.IP.PerHour = parseAppLimitInt64(content, "proxyIpPerHour", limits.Upload.Rate.Proxy.IP.PerHour)
	limits.Upload.Rate.Proxy.IP.PerDay = parseAppLimitInt64(content, "proxyIpPerDay", limits.Upload.Rate.Proxy.IP.PerDay)
	limits.Upload.Rate.Proxy.IP.PerWeek = parseAppLimitInt64(content, "proxyIpPerWeek", limits.Upload.Rate.Proxy.IP.PerWeek)
	limits.Upload.Rate.Proxy.IP.PerMonth = parseAppLimitInt64(content, "proxyIpPerMonth", limits.Upload.Rate.Proxy.IP.PerMonth)
	limits.Upload.Rate.Proxy.Device.PerHour = parseAppLimitInt64(content, "proxyDevicePerHour", limits.Upload.Rate.Proxy.Device.PerHour)
	limits.Upload.Rate.Proxy.Device.PerDay = parseAppLimitInt64(content, "proxyDevicePerDay", limits.Upload.Rate.Proxy.Device.PerDay)
	limits.Upload.Rate.Proxy.Device.PerWeek = parseAppLimitInt64(content, "proxyDevicePerWeek", limits.Upload.Rate.Proxy.Device.PerWeek)
	limits.Upload.Rate.Proxy.Device.PerMonth = parseAppLimitInt64(content, "proxyDevicePerMonth", limits.Upload.Rate.Proxy.Device.PerMonth)

	limits.WS.MaxMessageSize = parseAppLimitInt64(content, "maxMessageSize", limits.WS.MaxMessageSize)
	limits.WS.MaxTextChars = int(parseAppLimitInt64(content, "maxTextChars", int64(limits.WS.MaxTextChars)))
	limits.WS.MaxMediaURLLength = int(parseAppLimitInt64(content, "maxMediaURLLength", int64(limits.WS.MaxMediaURLLength)))
	limits.WS.MaxFileNameLength = int(parseAppLimitInt64(content, "maxFileNameLength", int64(limits.WS.MaxFileNameLength)))
	limits.WS.MaxGlobalConnections = int32(parseAppLimitInt64(content, "maxGlobalConnections", int64(limits.WS.MaxGlobalConnections)))
	limits.WS.MaxConnectionsPerIP = int32(parseAppLimitInt64(content, "maxConnectionsPerIP", int64(limits.WS.MaxConnectionsPerIP)))
	limits.WS.MaxConnectionsPerRoom = int(parseAppLimitInt64(content, "maxConnectionsPerRoom", int64(limits.WS.MaxConnectionsPerRoom)))

	limits.WS.Rate.ConnectUser.PerHour = parseAppLimitInt64(content, "connectUserPerHour", limits.WS.Rate.ConnectUser.PerHour)
	limits.WS.Rate.ConnectUser.PerDay = parseAppLimitInt64(content, "connectUserPerDay", limits.WS.Rate.ConnectUser.PerDay)
	limits.WS.Rate.ConnectUser.PerWeek = parseAppLimitInt64(content, "connectUserPerWeek", limits.WS.Rate.ConnectUser.PerWeek)
	limits.WS.Rate.ConnectUser.PerMonth = parseAppLimitInt64(content, "connectUserPerMonth", limits.WS.Rate.ConnectUser.PerMonth)
	limits.WS.Rate.ConnectIP.PerHour = parseAppLimitInt64(content, "connectIpPerHour", limits.WS.Rate.ConnectIP.PerHour)
	limits.WS.Rate.ConnectIP.PerDay = parseAppLimitInt64(content, "connectIpPerDay", limits.WS.Rate.ConnectIP.PerDay)
	limits.WS.Rate.ConnectIP.PerWeek = parseAppLimitInt64(content, "connectIpPerWeek", limits.WS.Rate.ConnectIP.PerWeek)
	limits.WS.Rate.ConnectIP.PerMonth = parseAppLimitInt64(content, "connectIpPerMonth", limits.WS.Rate.ConnectIP.PerMonth)
	limits.WS.Rate.ConnectDevice.PerHour = parseAppLimitInt64(content, "connectDevicePerHour", limits.WS.Rate.ConnectDevice.PerHour)
	limits.WS.Rate.ConnectDevice.PerDay = parseAppLimitInt64(content, "connectDevicePerDay", limits.WS.Rate.ConnectDevice.PerDay)
	limits.WS.Rate.ConnectDevice.PerWeek = parseAppLimitInt64(content, "connectDevicePerWeek", limits.WS.Rate.ConnectDevice.PerWeek)
	limits.WS.Rate.ConnectDevice.PerMonth = parseAppLimitInt64(content, "connectDevicePerMonth", limits.WS.Rate.ConnectDevice.PerMonth)

	return limits.normalized()
}

func parseAppLimitInt64(content string, field string, fallback int64) int64 {
	pattern := appLimitPattern(field, false)
	if pattern == nil {
		return fallback
	}
	matches := pattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return fallback
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(matches[1]), 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseAppLimitFloat64(content string, field string, fallback float64) float64 {
	pattern := appLimitPattern(field, true)
	if pattern == nil {
		return fallback
	}
	matches := pattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return fallback
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(matches[1]), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func appLimitPattern(field string, wantsFloat bool) *regexp.Regexp {
	normalizedField := strings.TrimSpace(field)
	if normalizedField == "" {
		return nil
	}
	cacheKey := normalizedField + "|int"
	regexBody := `(?m)\b` + regexp.QuoteMeta(normalizedField) + `\b\s*:\s*(-?\d+)`
	if wantsFloat {
		cacheKey = normalizedField + "|float"
		regexBody = `(?m)\b` + regexp.QuoteMeta(normalizedField) + `\b\s*:\s*(-?\d+(?:\.\d+)?)`
	}
	if cached, ok := appLimitPatternCache.Load(cacheKey); ok {
		if pattern, castOK := cached.(*regexp.Regexp); castOK {
			return pattern
		}
	}
	compiled := regexp.MustCompile(regexBody)
	appLimitPatternCache.Store(cacheKey, compiled)
	return compiled
}

func (v AppLimits) normalized() AppLimits {
	if v.AI.ContextMessageLimit <= 0 {
		v.AI.ContextMessageLimit = defaultAppLimits.AI.ContextMessageLimit
	}
	if v.AI.OrganizeMaxRequestBytes <= 0 {
		v.AI.OrganizeMaxRequestBytes = defaultAppLimits.AI.OrganizeMaxRequestBytes
	}
	if v.AI.OrganizeMaxItems <= 0 {
		v.AI.OrganizeMaxItems = defaultAppLimits.AI.OrganizeMaxItems
	}
	if v.AI.OrganizeNoteMaxLength <= 0 {
		v.AI.OrganizeNoteMaxLength = defaultAppLimits.AI.OrganizeNoteMaxLength
	}
	if v.AI.OrganizeTopicMaxLength <= 0 {
		v.AI.OrganizeTopicMaxLength = defaultAppLimits.AI.OrganizeTopicMaxLength
	}
	if v.AI.OrganizeTextMaxLength <= 0 {
		v.AI.OrganizeTextMaxLength = defaultAppLimits.AI.OrganizeTextMaxLength
	}
	if v.AI.OrganizeRequestTimeout <= 0 {
		v.AI.OrganizeRequestTimeout = defaultAppLimits.AI.OrganizeRequestTimeout
	}
	v.AI.UserRequestLimits = sanitizeWindowLimit(v.AI.UserRequestLimits)
	v.AI.RoomRequestLimits = sanitizeWindowLimit(v.AI.RoomRequestLimits)
	v.AI.IPRequestLimits = sanitizeWindowLimit(v.AI.IPRequestLimits)
	v.AI.DeviceRequestLimits = sanitizeWindowLimit(v.AI.DeviceRequestLimits)

	if v.Board.MaxStorageBytes <= 0 {
		v.Board.MaxStorageBytes = defaultAppLimits.Board.MaxStorageBytes
	}
	if v.Room.CodeDigits <= 0 {
		v.Room.CodeDigits = defaultAppLimits.Room.CodeDigits
	}
	if v.Room.NameMaxLength <= 0 {
		v.Room.NameMaxLength = defaultAppLimits.Room.NameMaxLength
	}
	if v.Room.PasswordMaxLength <= 0 {
		v.Room.PasswordMaxLength = defaultAppLimits.Room.PasswordMaxLength
	}
	if v.Room.MaxDescendants <= 0 {
		v.Room.MaxDescendants = defaultAppLimits.Room.MaxDescendants
	}
	if v.Room.MaxDurationHours <= 0 {
		v.Room.MaxDurationHours = defaultAppLimits.Room.MaxDurationHours
	}

	if v.Upload.MaxFileBytes <= 0 {
		v.Upload.MaxFileBytes = defaultAppLimits.Upload.MaxFileBytes
	}
	if v.Upload.MaxImageBytes <= 0 {
		v.Upload.MaxImageBytes = v.Upload.MaxFileBytes
	}
	if v.Upload.MaxMultipartBytes <= 0 {
		v.Upload.MaxMultipartBytes = v.Upload.MaxFileBytes + (1 * 1024 * 1024)
	}
	if v.Upload.MaxFormFieldLength <= 0 {
		v.Upload.MaxFormFieldLength = defaultAppLimits.Upload.MaxFormFieldLength
	}
	v.Upload.Rate.GenerateURL.User = sanitizeWindowLimit(v.Upload.Rate.GenerateURL.User)
	v.Upload.Rate.GenerateURL.IP = sanitizeWindowLimit(v.Upload.Rate.GenerateURL.IP)
	v.Upload.Rate.GenerateURL.Device = sanitizeWindowLimit(v.Upload.Rate.GenerateURL.Device)
	v.Upload.Rate.Proxy.User = sanitizeWindowLimit(v.Upload.Rate.Proxy.User)
	v.Upload.Rate.Proxy.IP = sanitizeWindowLimit(v.Upload.Rate.Proxy.IP)
	v.Upload.Rate.Proxy.Device = sanitizeWindowLimit(v.Upload.Rate.Proxy.Device)

	if v.WS.MaxMessageSize <= 0 {
		v.WS.MaxMessageSize = defaultAppLimits.WS.MaxMessageSize
	}
	if v.WS.MaxTextChars <= 0 {
		v.WS.MaxTextChars = defaultAppLimits.WS.MaxTextChars
	}
	if v.WS.MaxMediaURLLength <= 0 {
		v.WS.MaxMediaURLLength = defaultAppLimits.WS.MaxMediaURLLength
	}
	if v.WS.MaxFileNameLength <= 0 {
		v.WS.MaxFileNameLength = defaultAppLimits.WS.MaxFileNameLength
	}
	if v.WS.MaxGlobalConnections <= 0 {
		v.WS.MaxGlobalConnections = defaultAppLimits.WS.MaxGlobalConnections
	}
	if v.WS.MaxConnectionsPerIP <= 0 {
		v.WS.MaxConnectionsPerIP = defaultAppLimits.WS.MaxConnectionsPerIP
	}
	if v.WS.MaxConnectionsPerRoom <= 0 {
		v.WS.MaxConnectionsPerRoom = defaultAppLimits.WS.MaxConnectionsPerRoom
	}
	v.WS.Rate.ConnectUser = sanitizeWindowLimit(v.WS.Rate.ConnectUser)
	v.WS.Rate.ConnectIP = sanitizeWindowLimit(v.WS.Rate.ConnectIP)
	v.WS.Rate.ConnectDevice = sanitizeWindowLimit(v.WS.Rate.ConnectDevice)

	return v
}

func sanitizeWindowLimit(limit TimeWindowLimit) TimeWindowLimit {
	if limit.PerHour < 0 {
		limit.PerHour = 0
	}
	if limit.PerDay < 0 {
		limit.PerDay = 0
	}
	if limit.PerWeek < 0 {
		limit.PerWeek = 0
	}
	if limit.PerMonth < 0 {
		limit.PerMonth = 0
	}
	return limit
}
