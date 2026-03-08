package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/savanp08/converse/internal/config"
)

const presignedPutTTL = 10 * time.Minute

var (
	ErrUploadTooLarge = errors.New("upload exceeds size limit")
	ErrEmptyUpload    = errors.New("upload is empty")
)

type R2Client struct {
	Client        *minio.Client
	Bucket        string
	EndpointURL   string
	PublicBaseURL string
}

func NewR2Client(cfg config.Config) (*R2Client, error) {
	accountID := strings.TrimSpace(cfg.R2AccountId)
	accessKey := strings.TrimSpace(cfg.R2AccessKey)
	secretKey := strings.TrimSpace(cfg.R2SecretKey)
	bucket := strings.TrimSpace(cfg.R2Bucket)

	if accountID == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return nil, fmt.Errorf("missing required R2 credentials")
	}

	endpointHost := fmt.Sprintf("%s.r2.cloudflarestorage.com", accountID)
	client, err := minio.New(endpointHost, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: true,
		Region: "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("init r2 client: %w", err)
	}

	return &R2Client{
		Client:        client,
		Bucket:        bucket,
		EndpointURL:   "https://" + endpointHost,
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(cfg.R2PublicBaseURL), "/"),
	}, nil
}

func (r *R2Client) GetPresignedPutURL(filename string) (string, string, string, error) {
	if r == nil || r.Client == nil {
		return "", "", "", fmt.Errorf("r2 client is not configured")
	}

	safeName := sanitizeFilename(filename)
	fileID := uuid.NewString()
	objectKey := fmt.Sprintf("%s_%s", fileID, safeName)

	uploadURL, err := r.Client.PresignedPutObject(context.Background(), r.Bucket, objectKey, presignedPutTTL)
	if err != nil {
		return "", "", "", fmt.Errorf("presign put object: %w", err)
	}

	viewURL := r.buildViewURL(objectKey)

	return uploadURL.String(), viewURL, fileID, nil
}

func (r *R2Client) PutObject(
	ctx context.Context,
	filename string,
	reader io.Reader,
	contentType string,
	maxBytes int64,
) (string, string, int64, error) {
	if r == nil || r.Client == nil {
		return "", "", 0, fmt.Errorf("r2 client is not configured")
	}
	if reader == nil {
		return "", "", 0, fmt.Errorf("reader is required")
	}
	if maxBytes <= 0 {
		return "", "", 0, fmt.Errorf("maxBytes must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	safeName := sanitizeFilename(filename)
	fileID := uuid.NewString()
	objectKey := fmt.Sprintf("%s_%s", fileID, safeName)

	opts := minio.PutObjectOptions{
		ContentType: strings.TrimSpace(contentType),
	}
	if opts.ContentType == "" {
		opts.ContentType = "application/octet-stream"
	}

	limitedReader := newMaxBytesAbortReader(reader, maxBytes)
	uploadInfo, err := r.Client.PutObject(ctx, r.Bucket, objectKey, limitedReader, -1, opts)
	if err != nil {
		_ = r.removeObjectWithTimeout(objectKey)
		if limitedReader.Exceeded() {
			return "", "", 0, ErrUploadTooLarge
		}
		return "", "", 0, fmt.Errorf("put object: %w", err)
	}
	uploadedBytes := uploadInfo.Size
	if uploadedBytes <= 0 {
		uploadedBytes = limitedReader.BytesRead()
	}
	if limitedReader.Exceeded() || uploadedBytes > maxBytes {
		_ = r.removeObjectWithTimeout(objectKey)
		return "", "", 0, ErrUploadTooLarge
	}
	if uploadedBytes <= 0 {
		_ = r.removeObjectWithTimeout(objectKey)
		return "", "", 0, ErrEmptyUpload
	}

	return r.buildViewURL(objectKey), fileID, uploadedBytes, nil
}

type maxBytesAbortReader struct {
	reader    io.Reader
	maxBytes  int64
	bytesRead int64
	exceeded  bool
}

func newMaxBytesAbortReader(reader io.Reader, maxBytes int64) *maxBytesAbortReader {
	return &maxBytesAbortReader{
		reader:   reader,
		maxBytes: maxBytes,
	}
}

func (r *maxBytesAbortReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}

	remaining := r.maxBytes - r.bytesRead
	if remaining <= 0 {
		var probe [1]byte
		readBytes, err := r.reader.Read(probe[:])
		if readBytes > 0 {
			r.exceeded = true
			return 0, ErrUploadTooLarge
		}
		if errors.Is(err, io.EOF) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}

	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}

	readBytes, err := r.reader.Read(buffer)
	r.bytesRead += int64(readBytes)
	return readBytes, err
}

func (r *maxBytesAbortReader) Exceeded() bool {
	return r.exceeded
}

func (r *maxBytesAbortReader) BytesRead() int64 {
	return r.bytesRead
}

func (r *R2Client) removeObjectWithTimeout(objectKey string) error {
	if r == nil || r.Client == nil {
		return nil
	}

	key := strings.TrimSpace(strings.TrimPrefix(objectKey, "/"))
	if key == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return r.Client.RemoveObject(ctx, r.Bucket, key, minio.RemoveObjectOptions{})
}

func sanitizeFilename(raw string) string {
	base := strings.TrimSpace(path.Base(raw))
	if base == "" || base == "." || base == "/" {
		return "file"
	}

	var builder strings.Builder
	for _, ch := range base {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9'):
			builder.WriteRune(ch)
		case ch == '.' || ch == '_' || ch == '-':
			builder.WriteRune(ch)
		case ch == ' ':
			builder.WriteByte('_')
		}
	}

	safe := strings.Trim(builder.String(), "._")
	if safe == "" {
		return "file"
	}
	return safe
}

func (r *R2Client) buildViewURL(objectKey string) string {
	if r == nil {
		return ""
	}
	if r.PublicBaseURL == "" {
		return "/api/upload/object/" + url.PathEscape(objectKey)
	}
	return fmt.Sprintf("%s/%s", r.PublicBaseURL, objectKey)
}

func (r *R2Client) GetObject(ctx context.Context, objectKey string) (*minio.Object, minio.ObjectInfo, error) {
	if r == nil || r.Client == nil {
		return nil, minio.ObjectInfo{}, fmt.Errorf("r2 client is not configured")
	}

	obj, err := r.Client.GetObject(ctx, r.Bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, minio.ObjectInfo{}, fmt.Errorf("get object: %w", err)
	}

	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, minio.ObjectInfo{}, fmt.Errorf("stat object: %w", err)
	}

	return obj, info, nil
}

func (r *R2Client) DeleteObjects(ctx context.Context, objectKeys []string) error {
	if r == nil || r.Client == nil {
		return fmt.Errorf("r2 client is not configured")
	}
	if len(objectKeys) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(objectKeys))
	objectsCh := make(chan minio.ObjectInfo, len(objectKeys))
	for _, rawKey := range objectKeys {
		key := strings.TrimPrefix(strings.TrimSpace(rawKey), "/")
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		objectsCh <- minio.ObjectInfo{Key: key}
	}
	close(objectsCh)

	var firstErr error
	for removeErr := range r.Client.RemoveObjects(ctx, r.Bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if removeErr.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove object %s: %w", removeErr.ObjectName, removeErr.Err)
		}
	}
	return firstErr
}

func canvasSnapshotObjectKey(roomID string) (string, error) {
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return "", fmt.Errorf("room id is required")
	}
	return fmt.Sprintf("canvas/%s.yjs", normalizedRoomID), nil
}

func isR2ObjectNotFound(err error) bool {
	if err == nil {
		return false
	}
	errorResponse := minio.ToErrorResponse(err)
	switch strings.TrimSpace(errorResponse.Code) {
	case "NoSuchKey", "NoSuchObject":
		return true
	default:
		return false
	}
}

func SaveCanvasSnapshotToR2(
	ctx context.Context,
	s3Client *minio.Client,
	bucketName string,
	roomID string,
	snapshot []byte,
) error {
	if s3Client == nil {
		return fmt.Errorf("r2 client is not configured")
	}
	normalizedBucketName := strings.TrimSpace(bucketName)
	if normalizedBucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	key, err := canvasSnapshotObjectKey(roomID)
	if err != nil {
		return err
	}
	_, err = s3Client.PutObject(
		ctx,
		normalizedBucketName,
		key,
		bytes.NewReader(snapshot),
		int64(len(snapshot)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	if err != nil {
		return fmt.Errorf("put canvas snapshot object: %w", err)
	}
	return nil
}

func GetCanvasSnapshotFromR2(
	ctx context.Context,
	s3Client *minio.Client,
	bucketName string,
	roomID string,
) ([]byte, error) {
	if s3Client == nil {
		return nil, fmt.Errorf("r2 client is not configured")
	}
	normalizedBucketName := strings.TrimSpace(bucketName)
	if normalizedBucketName == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	key, err := canvasSnapshotObjectKey(roomID)
	if err != nil {
		return nil, err
	}

	object, err := s3Client.GetObject(ctx, normalizedBucketName, key, minio.GetObjectOptions{})
	if err != nil {
		if isR2ObjectNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get canvas snapshot object: %w", err)
	}
	defer object.Close()

	if _, err := object.Stat(); err != nil {
		if isR2ObjectNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat canvas snapshot object: %w", err)
	}

	snapshot, err := io.ReadAll(object)
	if err != nil {
		if isR2ObjectNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read canvas snapshot object: %w", err)
	}
	return snapshot, nil
}
