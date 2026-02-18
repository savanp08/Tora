package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
)

const usageTotalsID = "global"

type UsageLimits struct {
	MaxDailyRequests       int64 `json:"maxDailyRequests"`
	MaxDailyUploadBytes    int64 `json:"maxDailyUploadBytes"`
	MaxDailyBandwidthBytes int64 `json:"maxDailyBandwidthBytes"`
	MaxDailyMessages       int64 `json:"maxDailyMessages"`
	MaxDailyWsConnections  int64 `json:"maxDailyWsConnections"`
	MaxDailyFilesUploaded  int64 `json:"maxDailyFilesUploaded"`
}

type UsageSnapshot struct {
	Day            string    `json:"day"`
	RequestCount   int64     `json:"requestCount"`
	RequestBytes   int64     `json:"requestBytes"`
	BandwidthBytes int64     `json:"bandwidthBytes"`
	FilesUploaded  int64     `json:"filesUploaded"`
	UploadBytes    int64     `json:"uploadBytes"`
	WsConnections  int64     `json:"wsConnections"`
	WsMessages     int64     `json:"wsMessages"`
	Sleeping       bool      `json:"sleeping"`
	SleepReason    string    `json:"sleepReason,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type UsageTotals struct {
	RequestCount   int64     `json:"requestCount"`
	RequestBytes   int64     `json:"requestBytes"`
	BandwidthBytes int64     `json:"bandwidthBytes"`
	FilesUploaded  int64     `json:"filesUploaded"`
	UploadBytes    int64     `json:"uploadBytes"`
	WsConnections  int64     `json:"wsConnections"`
	WsMessages     int64     `json:"wsMessages"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type UsageReport struct {
	Current UsageSnapshot `json:"current"`
	Totals  UsageTotals   `json:"totals"`
	Limits  UsageLimits   `json:"limits"`
}

type UsageTracker struct {
	mu          sync.Mutex
	scylla      *database.ScyllaStore
	limits      UsageLimits
	current     UsageSnapshot
	totals      UsageTotals
	sleeping    bool
	sleepReason string
	persisted   map[string]struct{}
	stopCh      chan struct{}
	closeOnce   sync.Once
}

func NewUsageTracker(scylla *database.ScyllaStore, limits UsageLimits) *UsageTracker {
	now := time.Now().UTC()
	tracker := &UsageTracker{
		scylla: scylla,
		limits: limits,
		current: UsageSnapshot{
			Day:       now.Format("2006-01-02"),
			UpdatedAt: now,
		},
		totals: UsageTotals{
			UpdatedAt: now,
		},
		persisted: make(map[string]struct{}),
		stopCh:    make(chan struct{}),
	}
	tracker.ensureSchema()
	tracker.bootstrapFromStorage(now)
	go tracker.flushLoop()
	return tracker
}

func (t *UsageTracker) Close() {
	if t == nil {
		return
	}

	t.closeOnce.Do(func() {
		close(t.stopCh)

		now := time.Now().UTC()
		t.mu.Lock()
		rollover := t.rolloverLocked(now)
		snapshot := t.snapshotLocked()
		totals := t.totals
		totals.UpdatedAt = now
		t.mu.Unlock()

		if rollover != nil {
			t.persistSnapshot(*rollover)
		}
		t.persistSnapshot(snapshot)
		t.persistTotals(totals)
	})
}

func (t *UsageTracker) IsSleeping() bool {
	if t == nil {
		return false
	}

	now := time.Now().UTC()
	var rollover *UsageSnapshot

	t.mu.Lock()
	rollover = t.rolloverLocked(now)
	sleeping := t.sleeping
	t.mu.Unlock()

	if rollover != nil {
		go t.persistSnapshot(*rollover)
	}

	return sleeping
}

func (t *UsageTracker) Snapshot() UsageReport {
	if t == nil {
		return UsageReport{}
	}

	now := time.Now().UTC()
	var rollover *UsageSnapshot

	t.mu.Lock()
	rollover = t.rolloverLocked(now)
	snapshot := t.snapshotLocked()
	totals := t.totals
	t.mu.Unlock()

	if rollover != nil {
		go t.persistSnapshot(*rollover)
	}

	return UsageReport{
		Current: snapshot,
		Totals:  totals,
		Limits:  t.limits,
	}
}

func (t *UsageTracker) RecordRequest(requestBytes, bandwidthBytes int64) {
	t.record(func(snapshot *UsageSnapshot, totals *UsageTotals) {
		snapshot.RequestCount++
		totals.RequestCount++
		if requestBytes > 0 {
			snapshot.RequestBytes += requestBytes
			totals.RequestBytes += requestBytes
		}
		if bandwidthBytes > 0 {
			snapshot.BandwidthBytes += bandwidthBytes
			totals.BandwidthBytes += bandwidthBytes
		}
	})
}

func (t *UsageTracker) RecordUpload(uploadBytes int64) {
	t.record(func(snapshot *UsageSnapshot, totals *UsageTotals) {
		snapshot.FilesUploaded++
		totals.FilesUploaded++
		if uploadBytes > 0 {
			snapshot.UploadBytes += uploadBytes
			totals.UploadBytes += uploadBytes
		}
	})
}

func (t *UsageTracker) RecordDownload(downloadBytes int64) {
	t.record(func(snapshot *UsageSnapshot, totals *UsageTotals) {
		if downloadBytes > 0 {
			snapshot.BandwidthBytes += downloadBytes
			totals.BandwidthBytes += downloadBytes
		}
	})
}

func (t *UsageTracker) RecordWSConnection() {
	t.record(func(snapshot *UsageSnapshot, totals *UsageTotals) {
		snapshot.WsConnections++
		totals.WsConnections++
	})
}

func (t *UsageTracker) RecordWSMessage(messageBytes int64) {
	t.record(func(snapshot *UsageSnapshot, totals *UsageTotals) {
		snapshot.WsMessages++
		totals.WsMessages++
		if messageBytes > 0 {
			snapshot.BandwidthBytes += messageBytes
			totals.BandwidthBytes += messageBytes
		}
	})
}

func (t *UsageTracker) record(update func(snapshot *UsageSnapshot, totals *UsageTotals)) {
	if t == nil {
		return
	}

	now := time.Now().UTC()
	var rollover *UsageSnapshot
	var transitionedToSleep bool
	var sleepReason string

	t.mu.Lock()
	rollover = t.rolloverLocked(now)
	if update != nil {
		update(&t.current, &t.totals)
	}
	t.current.UpdatedAt = now
	t.totals.UpdatedAt = now
	transitionedToSleep, sleepReason = t.applyLimitsLocked()
	t.mu.Unlock()

	if rollover != nil {
		go t.persistSnapshot(*rollover)
	}

	if transitionedToSleep {
		log.Printf("[usage] safety sleep mode enabled reason=%s", sleepReason)
	}
}

func (t *UsageTracker) snapshotLocked() UsageSnapshot {
	snapshot := t.current
	snapshot.Sleeping = t.sleeping
	snapshot.SleepReason = t.sleepReason
	return snapshot
}

func (t *UsageTracker) rolloverLocked(now time.Time) *UsageSnapshot {
	currentDay := now.Format("2006-01-02")
	if t.current.Day == currentDay {
		return nil
	}

	previous := t.current
	previous.Sleeping = t.sleeping
	previous.SleepReason = t.sleepReason
	if previous.UpdatedAt.IsZero() {
		previous.UpdatedAt = now
	}

	t.current = UsageSnapshot{
		Day:       currentDay,
		UpdatedAt: now,
	}
	t.sleeping = false
	t.sleepReason = ""

	return &previous
}

func (t *UsageTracker) applyLimitsLocked() (bool, string) {
	if t.sleeping {
		return false, t.sleepReason
	}

	reason := ""
	if t.limits.MaxDailyRequests > 0 && t.current.RequestCount >= t.limits.MaxDailyRequests {
		reason = "daily request limit reached"
	} else if t.limits.MaxDailyUploadBytes > 0 && t.current.UploadBytes >= t.limits.MaxDailyUploadBytes {
		reason = "daily upload bytes limit reached"
	} else if t.limits.MaxDailyBandwidthBytes > 0 && t.current.BandwidthBytes >= t.limits.MaxDailyBandwidthBytes {
		reason = "daily bandwidth limit reached"
	} else if t.limits.MaxDailyMessages > 0 && t.current.WsMessages >= t.limits.MaxDailyMessages {
		reason = "daily websocket message limit reached"
	} else if t.limits.MaxDailyWsConnections > 0 && t.current.WsConnections >= t.limits.MaxDailyWsConnections {
		reason = "daily websocket connection limit reached"
	} else if t.limits.MaxDailyFilesUploaded > 0 && t.current.FilesUploaded >= t.limits.MaxDailyFilesUploaded {
		reason = "daily file upload count limit reached"
	}

	if reason == "" {
		return false, ""
	}
	t.sleeping = true
	t.sleepReason = reason
	return true, reason
}

func (t *UsageTracker) flushLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now().UTC()
			var rollover *UsageSnapshot

			t.mu.Lock()
			rollover = t.rolloverLocked(now)
			totals := t.totals
			totals.UpdatedAt = now
			t.mu.Unlock()

			if rollover != nil {
				go t.persistSnapshot(*rollover)
			}
			t.persistTotals(totals)
		case <-t.stopCh:
			return
		}
	}
}

func (t *UsageTracker) ensureSchema() {
	if t == nil || t.scylla == nil || t.scylla.Session == nil {
		return
	}

	usageDailyTable := t.scylla.Table("usage_daily")
	usageTotalsTable := t.scylla.Table("usage_totals")

	err := safeExecScyllaUsageQuery(
		t.scylla.Session,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			day text PRIMARY KEY,
			request_count bigint,
			request_bytes bigint,
			bandwidth_bytes bigint,
			files_uploaded bigint,
			upload_bytes bigint,
			ws_connections bigint,
			ws_messages bigint,
			sleep_activated boolean,
			sleep_reason text,
			updated_at timestamp
		)`, usageDailyTable),
	)
	if err != nil {
		log.Printf("[usage] failed to ensure usage_daily schema: %v", err)
	}

	err = safeExecScyllaUsageQuery(
		t.scylla.Session,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id text PRIMARY KEY,
			request_count bigint,
			request_bytes bigint,
			bandwidth_bytes bigint,
			files_uploaded bigint,
			upload_bytes bigint,
			ws_connections bigint,
			ws_messages bigint,
			updated_at timestamp
		)`, usageTotalsTable),
	)
	if err != nil {
		log.Printf("[usage] failed to ensure usage_totals schema: %v", err)
	}
}

func (t *UsageTracker) bootstrapFromStorage(now time.Time) {
	if t == nil || t.scylla == nil || t.scylla.Session == nil {
		return
	}

	usageDailyTable := t.scylla.Table("usage_daily")
	usageTotalsTable := t.scylla.Table("usage_totals")

	day := now.Format("2006-01-02")
	var current UsageSnapshot
	current.Day = day

	err := safeScanScyllaUsageQuery(
		t.scylla.Session,
		fmt.Sprintf(`SELECT request_count, request_bytes, bandwidth_bytes, files_uploaded, upload_bytes,
			ws_connections, ws_messages, sleep_activated, sleep_reason, updated_at
		FROM %s WHERE day = ? LIMIT 1`, usageDailyTable),
		[]interface{}{day},
		&current.RequestCount,
		&current.RequestBytes,
		&current.BandwidthBytes,
		&current.FilesUploaded,
		&current.UploadBytes,
		&current.WsConnections,
		&current.WsMessages,
		&current.Sleeping,
		&current.SleepReason,
		&current.UpdatedAt,
	)
	if err == nil {
		t.mu.Lock()
		if current.UpdatedAt.IsZero() {
			current.UpdatedAt = now
		}
		t.current = current
		t.sleeping = current.Sleeping
		t.sleepReason = current.SleepReason
		t.mu.Unlock()
	} else if err != gocql.ErrNotFound {
		log.Printf("[usage] load daily snapshot failed day=%s err=%v", day, err)
	}

	var totals UsageTotals
	err = safeScanScyllaUsageQuery(
		t.scylla.Session,
		fmt.Sprintf(`SELECT request_count, request_bytes, bandwidth_bytes, files_uploaded, upload_bytes,
			ws_connections, ws_messages, updated_at
		FROM %s WHERE id = ? LIMIT 1`, usageTotalsTable),
		[]interface{}{usageTotalsID},
		&totals.RequestCount,
		&totals.RequestBytes,
		&totals.BandwidthBytes,
		&totals.FilesUploaded,
		&totals.UploadBytes,
		&totals.WsConnections,
		&totals.WsMessages,
		&totals.UpdatedAt,
	)
	if err == nil {
		t.mu.Lock()
		if totals.UpdatedAt.IsZero() {
			totals.UpdatedAt = now
		}
		t.totals = totals
		t.mu.Unlock()
	} else if err != gocql.ErrNotFound {
		log.Printf("[usage] load totals failed err=%v", err)
	}
}

func (t *UsageTracker) persistSnapshot(snapshot UsageSnapshot) {
	if t == nil || snapshot.Day == "" || t.scylla == nil || t.scylla.Session == nil {
		return
	}

	if !t.beginPersist(snapshot.Day) {
		return
	}

	usageDailyTable := t.scylla.Table("usage_daily")

	err := safeExecScyllaUsageQuery(
		t.scylla.Session,
		fmt.Sprintf(`INSERT INTO %s (
			day, request_count, request_bytes, bandwidth_bytes, files_uploaded, upload_bytes,
			ws_connections, ws_messages, sleep_activated, sleep_reason, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, usageDailyTable),
		snapshot.Day,
		snapshot.RequestCount,
		snapshot.RequestBytes,
		snapshot.BandwidthBytes,
		snapshot.FilesUploaded,
		snapshot.UploadBytes,
		snapshot.WsConnections,
		snapshot.WsMessages,
		snapshot.Sleeping,
		snapshot.SleepReason,
		snapshot.UpdatedAt.UTC(),
	)
	if err != nil {
		log.Printf("[usage] failed to persist daily snapshot day=%s err=%v", snapshot.Day, err)
		t.rollbackPersist(snapshot.Day)
		return
	}
}

func (t *UsageTracker) persistTotals(totals UsageTotals) {
	if t == nil || t.scylla == nil || t.scylla.Session == nil {
		return
	}

	usageTotalsTable := t.scylla.Table("usage_totals")

	err := safeExecScyllaUsageQuery(
		t.scylla.Session,
		fmt.Sprintf(`INSERT INTO %s (
			id, request_count, request_bytes, bandwidth_bytes, files_uploaded,
			upload_bytes, ws_connections, ws_messages, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, usageTotalsTable),
		usageTotalsID,
		totals.RequestCount,
		totals.RequestBytes,
		totals.BandwidthBytes,
		totals.FilesUploaded,
		totals.UploadBytes,
		totals.WsConnections,
		totals.WsMessages,
		totals.UpdatedAt.UTC(),
	)
	if err != nil {
		log.Printf("[usage] failed to persist totals err=%v", err)
	}
}

func safeExecScyllaUsageQuery(session *gocql.Session, query string, args ...interface{}) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scylla query panic: %v", recovered)
		}
	}()
	return session.Query(query, args...).Exec()
}

func safeScanScyllaUsageQuery(session *gocql.Session, query string, args []interface{}, dest ...interface{}) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scylla query panic: %v", recovered)
		}
	}()
	return session.Query(query, args...).Scan(dest...)
}

func (t *UsageTracker) beginPersist(day string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.persisted[day]; exists {
		return false
	}
	t.persisted[day] = struct{}{}
	return true
}

func (t *UsageTracker) rollbackPersist(day string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.persisted, day)
}

func (t *UsageTracker) Middleware(next http.Handler) http.Handler {
	if t == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t.IsSleeping() && !isAllowedDuringSleep(r.URL.Path) {
			http.Error(w, "Server is in safety sleep mode", http.StatusServiceUnavailable)
			return
		}

		requestBytes := r.ContentLength
		if requestBytes < 0 {
			requestBytes = 0
		}

		recorder := &trackingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}
		next.ServeHTTP(recorder, r)
		t.RecordRequest(requestBytes, recorder.bytesWritten)
	})
}

func (t *UsageTracker) HandleUsage(w http.ResponseWriter, r *http.Request) {
	if t == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "usage tracker is unavailable",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(t.Snapshot())
}

func isAllowedDuringSleep(path string) bool {
	normalized := strings.TrimSpace(path)
	return normalized == "/health" || normalized == "/api/usage"
}

type trackingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (r *trackingResponseWriter) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *trackingResponseWriter) Write(payload []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	written, err := r.ResponseWriter.Write(payload)
	if written > 0 {
		r.bytesWritten += int64(written)
	}
	return written, err
}

func (r *trackingResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if readFrom, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		if r.statusCode == 0 {
			r.statusCode = http.StatusOK
		}
		written, err := readFrom.ReadFrom(reader)
		if written > 0 {
			r.bytesWritten += written
		}
		return written, err
	}
	return io.Copy(r, reader)
}

func (r *trackingResponseWriter) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *trackingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (r *trackingResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (r *trackingResponseWriter) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
