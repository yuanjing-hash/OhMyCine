package logging

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	DefaultQueryLimit       = 100
	MaxQueryLimit           = 200
	maxScanBytes      int64 = 64 * MiB
)

type Filter struct {
	From         time.Time
	To           time.Time
	Levels       []string
	Modules      []string
	Components   []string
	Operations   []string
	PluginIDs    []string
	Keyword      string
	RequestID    string
	TaskID       string
	LibraryID    string
	ConnectionID string
	StorageID    string
	DownloaderID string
	ScanRunID    string
	Limit        int
	Cursor       string
}

type Entry struct {
	Timestamp      time.Time      `json:"timestamp"`
	Level          string         `json:"level"`
	Message        string         `json:"message"`
	Module         string         `json:"module"`
	Component      string         `json:"component"`
	Operation      string         `json:"operation,omitempty"`
	OperationLabel string         `json:"operation_label,omitempty"`
	PluginID       string         `json:"plugin_id,omitempty"`
	Fields         map[string]any `json:"fields"`
}

type QueryResult struct {
	List         []Entry `json:"list"`
	NextCursor   string  `json:"next_cursor,omitempty"`
	ScannedBytes int64   `json:"scanned_bytes"`
	Malformed    int     `json:"malformed"`
	Partial      bool    `json:"partial"`
}
type Facets struct {
	Levels     []string `json:"levels"`
	Modules    []string `json:"modules"`
	Components []string `json:"components"`
	Operations []string `json:"operations"`
	PluginIDs  []string `json:"plugin_ids"`
}

func (f *Filter) Normalize(now time.Time) error {
	if f.To.IsZero() {
		f.To = now.UTC()
	}
	if f.From.IsZero() {
		f.From = f.To.Add(-24 * time.Hour)
	}
	if !f.From.Before(f.To) {
		return errors.New("from must be before to")
	}
	if f.To.Sub(f.From) > 31*24*time.Hour {
		return errors.New("time range must not exceed 31 days")
	}
	if len([]rune(f.Keyword)) > 128 {
		return errors.New("keyword must not exceed 128 characters")
	}
	if f.Limit <= 0 {
		f.Limit = DefaultQueryLimit
	}
	if f.Limit > MaxQueryLimit {
		return errors.New("limit must not exceed 200")
	}
	for _, level := range f.Levels {
		if level != "debug" && level != "info" && level != "warn" && level != "error" {
			return errors.New("invalid log level")
		}
	}
	return nil
}

func (m *Manager) Query(ctx context.Context, filter Filter) (QueryResult, error) {
	if err := filter.Normalize(time.Now().UTC()); err != nil {
		return QueryResult{}, err
	}
	offset, err := decodeCursor(filter)
	if err != nil {
		return QueryResult{}, err
	}
	files := m.queryFiles()
	result := QueryResult{List: make([]Entry, 0, filter.Limit)}
	matched := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		entries, bytesRead, malformed, readErr := readEntries(ctx, file)
		result.ScannedBytes += bytesRead
		result.Malformed += malformed
		if readErr != nil {
			result.Partial = true
			continue
		}
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			if !matches(entry, filter) {
				continue
			}
			if matched < offset {
				matched++
				continue
			}
			if len(result.List) == filter.Limit {
				result.NextCursor = encodeCursor(filter, matched)
				return result, nil
			}
			result.List = append(result.List, entry)
			matched++
		}
		if result.ScannedBytes >= maxScanBytes {
			result.Partial = true
			break
		}
	}
	return result, nil
}

func (m *Manager) Facets(ctx context.Context, filter Filter) (Facets, error) {
	filter.Limit = MaxQueryLimit
	filter.Cursor = ""
	result, err := m.Query(ctx, filter)
	if err != nil {
		return Facets{}, err
	}
	levels, modules, components, operations, plugins := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, entry := range result.List {
		levels[entry.Level] = struct{}{}
		modules[entry.Module] = struct{}{}
		components[entry.Component] = struct{}{}
		if entry.Operation != "" {
			operations[entry.Operation] = struct{}{}
		}
		if entry.PluginID != "" {
			plugins[entry.PluginID] = struct{}{}
		}
	}
	return Facets{Levels: sortedKeys(levels), Modules: sortedKeys(modules), Components: sortedKeys(components), Operations: sortedKeys(operations), PluginIDs: sortedKeys(plugins)}, nil
}

func (m *Manager) queryFiles() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	files := []string{}
	entries, _ := os.ReadDir(m.dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == activeName || (strings.HasPrefix(name, "runtime-") && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.gz"))) {
			files = append(files, filepath.Join(m.dir, name))
		}
	}
	sort.Slice(files, func(i, j int) bool {
		ii, _ := os.Stat(files[i])
		jj, _ := os.Stat(files[j])
		if ii == nil || jj == nil {
			return files[i] > files[j]
		}
		return ii.ModTime().After(jj.ModTime())
	})
	return files
}

func readEntries(ctx context.Context, path string) ([]Entry, int64, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = file.Close() }()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		gz, e := gzip.NewReader(file)
		if e != nil {
			return nil, 0, 0, e
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}
	limited := &io.LimitedReader{R: reader, N: maxScanBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	entries := []Entry{}
	malformed := 0
	var read int64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return entries, read, malformed, err
		}
		line := scanner.Bytes()
		read += int64(len(line) + 1)
		var raw map[string]any
		if json.Unmarshal(line, &raw) != nil {
			malformed++
			continue
		}
		entry, ok := entryFromRaw(raw)
		if ok {
			entries = append(entries, entry)
		} else {
			malformed++
		}
	}
	if err := scanner.Err(); err != nil {
		return entries, read, malformed, err
	}
	return entries, read, malformed, nil
}

func entryFromRaw(raw map[string]any) (Entry, bool) {
	tsText, _ := raw[zerologTimestampField()].(string)
	ts, err := time.Parse(time.RFC3339Nano, tsText)
	if err != nil {
		if number, ok := raw[zerologTimestampField()].(float64); ok {
			ts = time.UnixMilli(int64(number)).UTC()
		} else {
			return Entry{}, false
		}
	}
	level, _ := raw["level"].(string)
	msg, _ := raw["message"].(string)
	module, _ := raw["module"].(string)
	component, _ := raw["component"].(string)
	operation, _ := raw["operation"].(string)
	operationLabel, _ := raw["operation_label"].(string)
	plugin, _ := raw["plugin_id"].(string)
	fields := map[string]any{}
	for k, v := range raw {
		if k != "time" && k != "timestamp" && k != "level" && k != "message" && k != "module" && k != "component" && k != "operation" && k != "operation_label" && k != "plugin_id" {
			fields[k] = v
		}
	}
	return Entry{Timestamp: ts.UTC(), Level: level, Message: msg, Module: module, Component: component, Operation: operation, OperationLabel: operationLabel, PluginID: plugin, Fields: fields}, true
}
func zerologTimestampField() string { return zerolog.TimestampFieldName }
func matches(e Entry, f Filter) bool {
	if e.Timestamp.Before(f.From) || e.Timestamp.After(f.To) {
		return false
	}
	if !containsOrEmpty(f.Levels, e.Level) || !containsOrEmpty(f.Modules, e.Module) || !containsOrEmpty(f.Components, e.Component) || !containsOrEmpty(f.Operations, e.Operation) || !containsOrEmpty(f.PluginIDs, e.PluginID) {
		return false
	}
	if f.Keyword != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(f.Keyword)) {
		return false
	}
	checks := map[string]string{"request_id": f.RequestID, "task_id": f.TaskID, "library_id": f.LibraryID, "connection_id": f.ConnectionID, "storage_id": f.StorageID, "downloader_id": f.DownloaderID, "scan_run_id": f.ScanRunID}
	for k, want := range checks {
		if want != "" && fmt.Sprint(e.Fields[k]) != want {
			return false
		}
	}
	return true
}
func containsOrEmpty(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func sortedKeys(input map[string]struct{}) []string {
	out := make([]string, 0, len(input))
	for k := range input {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func filterHash(f Filter) string {
	clone := f
	clone.Cursor = ""
	clone.Limit = 0
	data, _ := json.Marshal(clone)
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}
func encodeCursor(f Filter, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(filterHash(f) + ":" + strconv.Itoa(offset)))
}
func decodeCursor(f Filter) (int, error) {
	if f.Cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 || parts[0] != filterHash(f) {
		return 0, errors.New("invalid cursor")
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return 0, errors.New("invalid cursor")
	}
	return offset, nil
}
