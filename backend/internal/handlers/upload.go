package handlers

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/storage"
)

const (
	maxUploadFileSize = 50 * 1024 * 1024
	maxImageFileSize  = 12 * 1024 * 1024
	maxMultipartBytes = maxUploadFileSize + (8 * 1024 * 1024)
)

var (
	uploadRequestLimiter = security.NewLimiter(24, time.Minute, 8, 15*time.Minute)
	uploadReadLimiter    = security.NewLimiter(240, time.Minute, 64, 15*time.Minute)
	uploadProxyLimiter   = security.NewLimiter(12, time.Minute, 4, 15*time.Minute)
)

type UploadHandler struct {
	r2      *storage.R2Client
	tracker *monitor.UsageTracker
}

type GenerateUploadURLRequest struct {
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
	FileSize int64  `json:"filesize"`
}

type GenerateUploadURLResponse struct {
	UploadURL string `json:"uploadUrl"`
	FileURL   string `json:"fileUrl"`
	FileID    string `json:"fileId"`
}

func NewUploadHandler(r2Client *storage.R2Client, tracker *monitor.UsageTracker) *UploadHandler {
	return &UploadHandler{r2: r2Client, tracker: tracker}
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
	if req.FileSize > maxUploadFileSize {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "File is larger than allowed limit (50MB)",
		})
		return
	}
	if strings.HasPrefix(fileType, "image/") && req.FileSize > maxImageFileSize {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Image is larger than allowed limit (12MB)",
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

	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBytes)
	if err := r.ParseMultipartForm(8 * 1024 * 1024); err != nil {
		message := "Invalid multipart upload payload"
		status := http.StatusBadRequest
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			status = http.StatusRequestEntityTooLarge
			message = "File is larger than allowed limit (50MB)"
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "file field is required"})
		return
	}
	defer file.Close()

	filename := strings.TrimSpace(header.Filename)
	if filename == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "filename is required"})
		return
	}

	fileSize := header.Size
	if fileSize < 0 {
		fileSize = 0
	}

	fileType := normalizeFileType(header.Header.Get("Content-Type"))
	if fileType == "" {
		fileType = normalizeFileType(mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))))
	}

	var reader io.Reader = file
	var payload []byte
	if fileSize == 0 {
		payload, err = io.ReadAll(io.LimitReader(file, maxUploadFileSize+1))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read uploaded file"})
			return
		}
		fileSize = int64(len(payload))
		reader = bytes.NewReader(payload)
		if fileType == "" && len(payload) > 0 {
			fileType = normalizeFileType(http.DetectContentType(payload))
		}
	}

	if fileSize <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "file must not be empty"})
		return
	}
	if fileSize > maxUploadFileSize {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "File is larger than allowed limit (50MB)",
		})
		return
	}
	if strings.HasPrefix(fileType, "image/") && fileSize > maxImageFileSize {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Image is larger than allowed limit (12MB)",
		})
		return
	}
	if !isAllowedUploadType(fileType) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Unsupported file type",
		})
		return
	}

	fileURL, fileID, err := h.r2.PutObject(r.Context(), filename, reader, fileSize, fileType)
	if err != nil {
		log.Printf("[upload] proxy upload failed filename=%s err=%v", filename, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to upload file"})
		return
	}

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
