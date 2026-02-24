package storage

import (
	"context"
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
	size int64,
	contentType string,
) (string, string, error) {
	if r == nil || r.Client == nil {
		return "", "", fmt.Errorf("r2 client is not configured")
	}
	if reader == nil {
		return "", "", fmt.Errorf("reader is required")
	}
	if size < 0 {
		return "", "", fmt.Errorf("size must be non-negative")
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

	if _, err := r.Client.PutObject(ctx, r.Bucket, objectKey, reader, size, opts); err != nil {
		return "", "", fmt.Errorf("put object: %w", err)
	}

	return r.buildViewURL(objectKey), fileID, nil
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
