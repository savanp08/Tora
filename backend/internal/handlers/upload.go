package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	neturl "net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/minio/minio-go/v7"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/storage"
)

var (
	uploadRequestLimiter = security.NewLimiter(24, time.Minute, 8, 15*time.Minute)
	uploadReadLimiter    = security.NewLimiter(240, time.Minute, 64, 15*time.Minute)
	uploadProxyLimiter   = security.NewLimiter(12, time.Minute, 4, 15*time.Minute)
)

const (
	uploadScopeUser   = "user"
	uploadScopeIP     = "ip"
	uploadScopeDevice = "device"
)

type uploadRateLimitRule struct {
	WindowName string
	Window     time.Duration
	MaxAllowed int64
}

type uploadRateLimitCheck struct {
	Scope string
	Value string
}

type uploadRateLimitExceededError struct {
	Action string
	Scope  string
	Window string
	Limit  int64
}

func (e *uploadRateLimitExceededError) Error() string {
	if e == nil {
		return "upload rate limit exceeded"
	}
	return fmt.Sprintf(
		"upload rate limit exceeded action=%s scope=%s window=%s limit=%d",
		strings.TrimSpace(e.Action),
		strings.TrimSpace(e.Scope),
		strings.TrimSpace(e.Window),
		e.Limit,
	)
}

func (e *uploadRateLimitExceededError) PublicMessage() string {
	if e == nil {
		return "Upload rate limit exceeded. Please try again later."
	}
	scopeLabel := "this context"
	switch strings.TrimSpace(e.Scope) {
	case uploadScopeUser:
		scopeLabel = "this user"
	case uploadScopeIP:
		scopeLabel = "this IP"
	case uploadScopeDevice:
		scopeLabel = "this device"
	}
	windowLabel := strings.TrimSpace(e.Window)
	if windowLabel == "" {
		windowLabel = "current"
	}
	return fmt.Sprintf("Upload rate limit exceeded for %s in the %s window.", scopeLabel, windowLabel)
}

func uploadLimits() config.UploadLimits {
	return config.LoadAppLimits().Upload
}

func maxUploadFileSize() int64 {
	return uploadLimits().MaxFileBytes
}

func maxImageFileSize() int64 {
	return uploadLimits().MaxImageBytes
}

func maxMultipartBytes() int64 {
	return uploadLimits().MaxMultipartBytes
}

func maxFormFieldLength() int64 {
	return uploadLimits().MaxFormFieldLength
}

func formatBinaryLimitMB(bytes int64) string {
	if bytes <= 0 {
		return "0MB"
	}
	mb := float64(bytes) / (1024 * 1024)
	if mb == float64(int64(mb)) {
		return fmt.Sprintf("%dMB", int64(mb))
	}
	return fmt.Sprintf("%.1fMB", mb)
}

func uploadLimitExceededMessage(kind string, maxBytes int64) string {
	label := strings.TrimSpace(kind)
	if label == "" {
		label = "File"
	}
	return fmt.Sprintf("%s is larger than allowed limit (%s)", label, formatBinaryLimitMB(maxBytes))
}

type UploadHandler struct {
	r2      *storage.R2Client
	redis   *database.RedisStore
	tracker *monitor.UsageTracker
}

type GenerateUploadURLRequest struct {
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
	FileSize int64  `json:"filesize"`
	RoomID   string `json:"roomId,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
}

type GenerateUploadURLResponse struct {
	UploadURL string `json:"uploadUrl"`
	FileURL   string `json:"fileUrl"`
	FileID    string `json:"fileId"`
}

func NewUploadHandler(
	r2Client *storage.R2Client,
	redisStore *database.RedisStore,
	tracker *monitor.UsageTracker,
) *UploadHandler {
	return &UploadHandler{r2: r2Client, redis: redisStore, tracker: tracker}
}

func (h *UploadHandler) GenerateUploadURL(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.r2 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload service is not configured",
		})
		return
	}
	if h.tracker != nil && h.tracker.IsSleeping() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Server is in safety sleep mode",
		})
		return
	}

	clientIP := extractClientIP(r)
	if !uploadRequestLimiter.Allow(clientIP) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload rate limit exceeded",
		})
		return
	}

	var req GenerateUploadURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid JSON format",
		})
		return
	}

	rateLimitUserID := extractUploadRateLimitUserID(r)
	rateLimitDeviceID := extractUploadRateLimitDeviceID(r, req.DeviceID)
	if err := enforceUploadActionRateLimits(r.Context(), "generate_url", rateLimitUserID, clientIP, rateLimitDeviceID); err != nil {
		var exceeded *uploadRateLimitExceededError
		if errors.As(err, &exceeded) {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": exceeded.PublicMessage(),
			})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload limiter unavailable",
		})
		return
	}

	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "filename is required",
		})
		return
	}

	fileType := normalizeFileType(req.FileType)
	if !isAllowedUploadType(fileType) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Unsupported file type",
		})
		return
	}
	if req.FileSize <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "filesize is required",
		})
		return
	}
	if req.FileSize > maxUploadFileSize() {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": uploadLimitExceededMessage("File", maxUploadFileSize()),
		})
		return
	}
	if strings.HasPrefix(fileType, "image/") && req.FileSize > maxImageFileSize() {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": uploadLimitExceededMessage("Image", maxImageFileSize()),
		})
		return
	}

	uploadURL, fileURL, fileID, err := h.r2.GetPresignedPutURL(filename)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to generate upload URL",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(GenerateUploadURLResponse{
		UploadURL: uploadURL,
		FileURL:   fileURL,
		FileID:    fileID,
	})

	normalizedRoomID := normalizeRoomID(req.RoomID)
	if normalizedRoomID != "" {
		objectKey := h.resolveObjectKeyFromFileURL(fileURL)
		h.trackUploadedFile(r.Context(), normalizedRoomID, objectKey)
	}

	if h.tracker != nil {
		h.tracker.RecordUpload(req.FileSize)
	}
}

func (h *UploadHandler) UploadProxy(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.r2 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload service is not configured",
		})
		return
	}
	if h.tracker != nil && h.tracker.IsSleeping() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Server is in safety sleep mode",
		})
		return
	}

	clientIP := extractClientIP(r)
	if !uploadProxyLimiter.Allow(clientIP) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload rate limit exceeded",
		})
		return
	}
	if err := enforceUploadActionRateLimits(
		r.Context(),
		"proxy",
		extractUploadRateLimitUserID(r),
		clientIP,
		extractUploadRateLimitDeviceID(r, ""),
	); err != nil {
		var exceeded *uploadRateLimitExceededError
		if errors.As(err, &exceeded) {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": exceeded.PublicMessage(),
			})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload limiter unavailable",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBytes())
	multipartReader, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid multipart upload payload"})
		return
	}

	var (
		fileURL        string
		fileID         string
		fileSize       int64
		roomIDFromForm string
		uploaded       bool
	)

	for {
		part, partErr := multipartReader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			message := "Invalid multipart upload payload"
			status := http.StatusBadRequest
			if strings.Contains(strings.ToLower(partErr.Error()), "too large") {
				status = http.StatusRequestEntityTooLarge
				message = uploadLimitExceededMessage("File", maxUploadFileSize())
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
			return
		}

		fieldName := strings.TrimSpace(part.FormName())
		switch fieldName {
		case "roomId":
			if roomIDFromForm == "" {
				value, readErr := readSmallMultipartField(part, maxFormFieldLength())
				if readErr != nil {
					_ = part.Close()
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "roomId field is invalid"})
					return
				}
				roomIDFromForm = value
			}
			_ = part.Close()
		case "file":
			if uploaded {
				_ = part.Close()
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Only one file can be uploaded at a time"})
				return
			}

			filename := strings.TrimSpace(part.FileName())
			if filename == "" {
				_ = part.Close()
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "filename is required"})
				return
			}

			fileType := normalizeFileType(part.Header.Get("Content-Type"))
			if fileType == "" {
				fileType = normalizeFileType(mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))))
			}

			fileReader, detectedFileType, detectErr := detectUploadContentType(part, fileType)
			if detectErr != nil {
				_ = part.Close()
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to inspect uploaded file"})
				return
			}
			fileType = normalizeFileType(detectedFileType)

			if !isAllowedUploadType(fileType) {
				_ = part.Close()
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "Unsupported file type",
				})
				return
			}

			var uploadErr error
			fileURL, fileID, fileSize, uploadErr = h.r2.PutObject(
				r.Context(),
				filename,
				fileReader,
				fileType,
				maxUploadFileSize(),
			)
			_ = part.Close()
			if uploadErr != nil {
				monitor.TotalUploads.WithLabelValues("error").Inc()
				log.Printf("[upload] proxy upload failed filename=%s err=%v", filename, uploadErr)
				switch {
				case errors.Is(uploadErr, storage.ErrUploadTooLarge):
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": uploadLimitExceededMessage("File", maxUploadFileSize()),
					})
				case errors.Is(uploadErr, storage.ErrEmptyUpload):
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "file must not be empty"})
				default:
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to upload file"})
				}
				return
			}

			if strings.HasPrefix(fileType, "image/") && fileSize > maxImageFileSize() {
				_ = h.r2.DeleteObjects(r.Context(), []string{h.resolveObjectKeyFromFileURL(fileURL)})
				monitor.TotalUploads.WithLabelValues("error").Inc()
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": uploadLimitExceededMessage("Image", maxImageFileSize()),
				})
				return
			}
			uploaded = true
		default:
			_ = part.Close()
		}
	}

	if !uploaded {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "file field is required"})
		return
	}

	monitor.TotalUploads.WithLabelValues("success").Inc()
	monitor.UploadBytes.Observe(float64(fileSize))

	alreadyCounted := strings.TrimSpace(r.URL.Query().Get("counted")) == "1"
	if h.tracker != nil && !alreadyCounted {
		h.tracker.RecordUpload(fileSize)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(GenerateUploadURLResponse{
		UploadURL: "",
		FileURL:   fileURL,
		FileID:    fileID,
	})

	normalizedRoomID := normalizeRoomID(firstNonEmpty(r.URL.Query().Get("roomId"), roomIDFromForm))
	if normalizedRoomID != "" {
		objectKey := h.resolveObjectKeyFromFileURL(fileURL)
		h.trackUploadedFile(r.Context(), normalizedRoomID, objectKey)
	}
}

func (h *UploadHandler) ServeObject(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.r2 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Upload service is not configured",
		})
		return
	}
	if h.tracker != nil && h.tracker.IsSleeping() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Server is in safety sleep mode",
		})
		return
	}

	clientIP := extractClientIP(r)
	if !uploadReadLimiter.Allow(clientIP) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Download rate limit exceeded",
		})
		return
	}

	escapedKey := strings.TrimSpace(chi.URLParam(r, "*"))
	if escapedKey == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "object key is required",
		})
		return
	}

	objectKey, err := neturl.PathUnescape(escapedKey)
	if err != nil || strings.Contains(objectKey, "..") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid object key",
		})
		return
	}

	obj, info, keyUsed, err := h.loadObjectWithFallback(r.Context(), objectKey)
	if err != nil {
		log.Printf("[upload] object not found key=%s bucket=%s err=%v", objectKey, h.r2.Bucket, err)
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "file not found",
		})
		return
	}
	defer obj.Close()

	if info.ContentType != "" {
		w.Header().Set("Content-Type", info.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	if !info.LastModified.IsZero() {
		w.Header().Set("Last-Modified", info.LastModified.UTC().Format(http.TimeFormat))
	}
	filename := sanitizeHeaderFilename(path.Base(keyUsed))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "private, max-age=600")

	written, copyErr := io.Copy(w, obj)
	if copyErr != nil {
		return
	}
	if h.tracker != nil {
		h.tracker.RecordDownload(written)
	}
}

func (h *UploadHandler) loadObjectWithFallback(
	ctx context.Context,
	objectKey string,
) (*minio.Object, minio.ObjectInfo, string, error) {
	keys := candidateObjectKeys(objectKey, h.r2.Bucket)
	var lastErr error
	for _, key := range keys {
		obj, info, err := h.r2.GetObject(ctx, key)
		if err == nil {
			return obj, info, key, nil
		}
		lastErr = err
	}
	return nil, minio.ObjectInfo{}, "", lastErr
}

func candidateObjectKeys(rawKey, bucket string) []string {
	key := strings.TrimSpace(strings.TrimPrefix(rawKey, "/"))
	if key == "" {
		return []string{}
	}

	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(v string) {
		value := strings.TrimSpace(strings.TrimPrefix(v, "/"))
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}

	add(key)

	if bucketTrimmed := strings.TrimSpace(strings.Trim(rawKey, "/")); strings.HasPrefix(bucketTrimmed, bucket+"/") {
		add(strings.TrimPrefix(bucketTrimmed, bucket+"/"))
	}

	decoded, err := neturl.PathUnescape(key)
	if err == nil && decoded != key {
		add(decoded)
		if strings.HasPrefix(decoded, bucket+"/") {
			add(strings.TrimPrefix(decoded, bucket+"/"))
		}
	}

	return candidates
}

func normalizeFileType(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func detectUploadContentType(reader io.Reader, fallbackType string) (io.Reader, string, error) {
	normalizedFallback := normalizeFileType(fallbackType)
	if normalizedFallback != "" {
		return reader, normalizedFallback, nil
	}

	header := make([]byte, 512)
	readBytes, err := io.ReadFull(reader, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, "", err
	}

	detectedType := normalizedFallback
	if readBytes > 0 {
		detectedType = normalizeFileType(http.DetectContentType(header[:readBytes]))
	}

	return io.MultiReader(bytes.NewReader(header[:readBytes]), reader), detectedType, nil
}

func readSmallMultipartField(reader io.Reader, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = maxFormFieldLength()
	}

	var builder strings.Builder
	written, err := io.Copy(&builder, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return "", err
	}
	if written > maxBytes {
		return "", fmt.Errorf("multipart field exceeds %d bytes", maxBytes)
	}
	return strings.TrimSpace(builder.String()), nil
}

func isAllowedUploadType(fileType string) bool {
	if fileType == "" {
		return false
	}
	if strings.HasPrefix(fileType, "image/") ||
		strings.HasPrefix(fileType, "video/") ||
		strings.HasPrefix(fileType, "audio/") {
		return true
	}

	switch fileType {
	case "application/pdf",
		"text/plain",
		"application/zip",
		"application/x-zip-compressed",
		"application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-excel",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	default:
		return false
	}
}

func sanitizeHeaderFilename(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "file"
	}
	replaced := strings.ReplaceAll(trimmed, "\"", "")
	replaced = strings.ReplaceAll(replaced, "\n", "")
	replaced = strings.ReplaceAll(replaced, "\r", "")
	if replaced == "" {
		return "file"
	}
	return replaced
}

func (h *UploadHandler) resolveObjectKeyFromFileURL(fileURL string) string {
	if h == nil {
		return ""
	}
	trimmed := strings.TrimSpace(fileURL)
	if trimmed == "" {
		return ""
	}

	const localPrefix = "/api/upload/object/"
	extractLocal := func(value string) string {
		escaped := strings.TrimPrefix(value, localPrefix)
		escaped = strings.TrimPrefix(escaped, "/")
		if escaped == "" {
			return ""
		}
		if decoded, err := neturl.PathUnescape(escaped); err == nil {
			return strings.TrimPrefix(strings.TrimSpace(decoded), "/")
		}
		return strings.TrimPrefix(strings.TrimSpace(escaped), "/")
	}

	if strings.HasPrefix(trimmed, localPrefix) {
		return extractLocal(trimmed)
	}

	parsed, err := neturl.Parse(trimmed)
	if err != nil {
		return ""
	}
	pathValue := strings.TrimSpace(parsed.Path)
	if pathValue == "" {
		return ""
	}
	if strings.HasPrefix(pathValue, localPrefix) {
		return extractLocal(pathValue)
	}

	key := strings.TrimPrefix(pathValue, "/")
	if h.r2 != nil && strings.TrimSpace(h.r2.Bucket) != "" {
		bucketPrefix := strings.TrimSpace(h.r2.Bucket) + "/"
		key = strings.TrimPrefix(key, bucketPrefix)
	}
	return strings.TrimSpace(key)
}

func (h *UploadHandler) trackUploadedFile(ctx context.Context, roomID, objectKey string) {
	if h == nil || h.redis == nil || h.redis.Client == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedObjectKey := strings.TrimSpace(objectKey)
	if normalizedRoomID == "" || normalizedObjectKey == "" {
		return
	}

	roomRedisKey := roomKey(normalizedRoomID)
	exists, err := h.redis.Client.Exists(ctx, roomRedisKey).Result()
	if err != nil || exists == 0 {
		return
	}

	filesKey := roomFilesKey(normalizedRoomID)
	if err := h.redis.Client.SAdd(ctx, filesKey, normalizedObjectKey).Err(); err != nil {
		log.Printf("[upload] failed to index room file room=%s key=%s err=%v", normalizedRoomID, normalizedObjectKey, err)
		return
	}

	const roomFilesGraceTTL = 5 * time.Minute
	roomTTL, ttlErr := h.redis.Client.TTL(ctx, roomRedisKey).Result()
	nextTTL := roomFilesGraceTTL
	if ttlErr == nil && roomTTL > 0 {
		nextTTL = roomTTL + roomFilesGraceTTL
	}
	if err := h.redis.Client.Expire(ctx, filesKey, nextTTL).Err(); err != nil {
		log.Printf("[upload] failed to set room file index ttl room=%s key=%s err=%v", normalizedRoomID, filesKey, err)
	}
}

func enforceUploadActionRateLimits(
	ctx context.Context,
	action string,
	userID string,
	ipAddress string,
	deviceID string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	limits := uploadLimits().Rate

	var scopeLimits config.UploadRateScopeLimits
	switch strings.TrimSpace(strings.ToLower(action)) {
	case "generate_url":
		scopeLimits = limits.GenerateURL
	case "proxy":
		scopeLimits = limits.Proxy
	default:
		return nil
	}

	checks := []uploadRateLimitCheck{
		{Scope: uploadScopeUser, Value: normalizeIdentifier(userID)},
		{Scope: uploadScopeIP, Value: strings.TrimSpace(ipAddress)},
		{Scope: uploadScopeDevice, Value: normalizeDeviceIdentifier(deviceID)},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.Value) == "" {
			continue
		}
		var rules []uploadRateLimitRule
		switch check.Scope {
		case uploadScopeUser:
			rules = buildUploadRateLimitRules(scopeLimits.User)
		case uploadScopeIP:
			rules = buildUploadRateLimitRules(scopeLimits.IP)
		case uploadScopeDevice:
			rules = buildUploadRateLimitRules(scopeLimits.Device)
		}

		for _, rule := range rules {
			result, err := security.AllowFixedWindow(
				ctx,
				"upload:"+action,
				check.Scope,
				rule.WindowName,
				check.Value,
				rule.MaxAllowed,
				rule.Window,
			)
			if err != nil {
				return err
			}
			if result.Allowed {
				continue
			}
			return &uploadRateLimitExceededError{
				Action: action,
				Scope:  check.Scope,
				Window: rule.WindowName,
				Limit:  rule.MaxAllowed,
			}
		}
	}
	return nil
}

func buildUploadRateLimitRules(limit config.TimeWindowLimit) []uploadRateLimitRule {
	rules := make([]uploadRateLimitRule, 0, 4)
	if limit.PerHour > 0 {
		rules = append(rules, uploadRateLimitRule{
			WindowName: "hour",
			Window:     time.Hour,
			MaxAllowed: limit.PerHour,
		})
	}
	if limit.PerDay > 0 {
		rules = append(rules, uploadRateLimitRule{
			WindowName: "day",
			Window:     24 * time.Hour,
			MaxAllowed: limit.PerDay,
		})
	}
	if limit.PerWeek > 0 {
		rules = append(rules, uploadRateLimitRule{
			WindowName: "week",
			Window:     7 * 24 * time.Hour,
			MaxAllowed: limit.PerWeek,
		})
	}
	if limit.PerMonth > 0 {
		rules = append(rules, uploadRateLimitRule{
			WindowName: "month",
			Window:     30 * 24 * time.Hour,
			MaxAllowed: limit.PerMonth,
		})
	}
	return rules
}

func extractUploadRateLimitUserID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return normalizeIdentifier(firstNonEmpty(
		r.URL.Query().Get("userId"),
		r.URL.Query().Get("user_id"),
		r.Header.Get("X-User-Id"),
	))
}

func extractUploadRateLimitDeviceID(r *http.Request, explicit string) string {
	if r == nil {
		return normalizeDeviceIdentifier(explicit)
	}
	return normalizeDeviceIdentifier(firstNonEmpty(
		explicit,
		r.URL.Query().Get("deviceId"),
		r.URL.Query().Get("device_id"),
		r.Header.Get("X-Device-Id"),
		r.Header.Get("X-Device-ID"),
	))
}

func extractClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}

	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}
