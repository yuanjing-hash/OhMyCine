package logging

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

const activeName = "runtime.jsonl"

const maxEventBytes = 64 * 1024

type Health struct {
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
}

type Manager struct {
	mu                   sync.Mutex
	dir                  string
	stdout               io.Writer
	file                 *os.File
	size                 int64
	policy               Policy
	level                zerolog.Level
	sequence             uint64
	degraded             atomic.Bool
	reason               atomic.Value
	lastDiagnostic       time.Time
	lastDiagnosticReason string
	stop                 chan struct{}
	done                 chan struct{}
}

func NewManager(directory, environment string, stdout io.Writer) (*Manager, error) {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	if stdout == nil {
		stdout = os.Stdout
	}
	m := &Manager{dir: filepath.Clean(directory), stdout: stdout, policy: DefaultPolicy(environment), stop: make(chan struct{}), done: make(chan struct{})}
	m.level = parseLevel(m.policy.Level)
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		m.markDegraded("file_sink_unavailable")
		go m.cleanLoop()
		return m, nil
	}
	if err := m.recoverAndOpen(); err != nil {
		m.markDegraded("file_sink_unavailable")
	}
	go m.cleanLoop()
	return m, nil
}

func (m *Manager) Logger(module, component string) zerolog.Logger {
	return zerolog.New(m).With().Timestamp().Str("module", module).Str("component", component).Logger()
}

func (m *Manager) PluginLogger(pluginID, component string) zerolog.Logger {
	return m.Logger("plugin", component).Hook(pluginIdentityHook{pluginID: pluginID})
}

type pluginIdentityHook struct{ pluginID string }

func (h pluginIdentityHook) Run(event *zerolog.Event, _ zerolog.Level, _ string) {
	event.Str("plugin_id", h.pluginID)
}

func (m *Manager) Directory() string { return m.dir }
func (m *Manager) Policy() Policy    { m.mu.Lock(); defer m.mu.Unlock(); return m.policy }
func (m *Manager) Health() Health {
	h := Health{Degraded: m.degraded.Load()}
	if v := m.reason.Load(); v != nil {
		h.Reason, _ = v.(string)
	}
	return h
}

func (m *Manager) Apply(policy Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.policy = policy
	m.level = parseLevel(policy.Level)
	err := m.cleanupLocked(time.Now().UTC())
	m.mu.Unlock()
	return err
}

func (m *Manager) Write(p []byte) (int, error) {
	clean := SanitizeJSON(p)
	m.mu.Lock()
	defer m.mu.Unlock()
	if eventLevel(clean) < m.level {
		return len(p), nil
	}
	_, _ = m.stdout.Write(clean)
	if m.file == nil {
		return len(p), nil
	}
	if m.size > 0 && m.size+int64(len(clean)) > int64(m.policy.MaxFileMiB)*MiB {
		if err := m.rotateLocked(); err != nil {
			m.markDegradedLocked("rotation_failed")
			return len(p), nil
		}
	}
	n, err := m.file.Write(clean)
	m.size += int64(n)
	if err != nil {
		m.markDegradedLocked("file_write_failed")
	}
	return len(p), nil
}

func (m *Manager) Close() error {
	close(m.stop)
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file != nil {
		return m.file.Close()
	}
	return nil
}

func (m *Manager) recoverAndOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, _ := os.ReadDir(m.dir)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "runtime-") && strings.HasSuffix(entry.Name(), ".jsonl") {
			_ = compressFile(filepath.Join(m.dir, entry.Name()))
		}
	}
	file, err := os.OpenFile(filepath.Join(m.dir, activeName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	m.file, m.size = file, info.Size()
	return m.cleanupLocked(time.Now().UTC())
}

func (m *Manager) rotateLocked() error {
	if m.file == nil {
		return nil
	}
	if err := m.file.Close(); err != nil {
		return err
	}
	m.sequence++
	base := fmt.Sprintf("runtime-%s-%06d.jsonl", time.Now().UTC().Format("20060102T150405.000000000Z"), m.sequence)
	rotated := filepath.Join(m.dir, base)
	if err := os.Rename(filepath.Join(m.dir, activeName), rotated); err != nil {
		m.file, _ = os.OpenFile(filepath.Join(m.dir, activeName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		return err
	}
	file, err := os.OpenFile(filepath.Join(m.dir, activeName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	m.file, m.size = file, 0
	if err := compressFile(rotated); err != nil {
		return err
	}
	return m.cleanupLocked(time.Now().UTC())
}

func compressFile(source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := source + ".gz.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	_, copyErr := io.Copy(zw, in)
	closeErr := zw.Close()
	fileCloseErr := out.Close()
	if copyErr != nil || closeErr != nil || fileCloseErr != nil {
		_ = os.Remove(tmp)
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return fileCloseErr
	}
	if err := os.Rename(tmp, source+".gz"); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Remove(source)
}

type managedFile struct {
	path string
	mod  time.Time
	size int64
}

func (m *Manager) managedHistoryLocked() []managedFile {
	entries, _ := os.ReadDir(m.dir)
	files := make([]managedFile, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "runtime-") || (!strings.HasSuffix(e.Name(), ".jsonl.gz") && !strings.HasSuffix(e.Name(), ".jsonl")) {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, managedFile{filepath.Join(m.dir, e.Name()), info.ModTime(), info.Size()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	return files
}

func (m *Manager) cleanupLocked(now time.Time) error {
	files := m.managedHistoryLocked()
	cutoff := now.Add(-time.Duration(m.policy.RetentionDays) * 24 * time.Hour)
	var total int64
	if info, err := os.Stat(filepath.Join(m.dir, activeName)); err == nil {
		total += info.Size()
	}
	for _, f := range files {
		total += f.size
	}
	for len(files) > 0 {
		oldest := files[0]
		if !oldest.mod.Before(cutoff) && len(files) <= m.policy.MaxBackups && total <= int64(m.policy.MaxTotalMiB)*MiB {
			break
		}
		if err := os.Remove(oldest.path); err != nil {
			return err
		}
		total -= oldest.size
		files = files[1:]
	}
	return nil
}

func (m *Manager) cleanLoop() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	defer close(m.done)
	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			if err := m.cleanupLocked(time.Now().UTC()); err != nil {
				m.markDegradedLocked("retention_failed")
			}
			m.mu.Unlock()
		case <-m.stop:
			return
		}
	}
}
func (m *Manager) markDegraded(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markDegradedLocked(reason)
}
func (m *Manager) markDegradedLocked(reason string) {
	m.degraded.Store(true)
	m.reason.Store(reason)
	if reason == m.lastDiagnosticReason && time.Since(m.lastDiagnostic) < time.Minute {
		return
	}
	m.lastDiagnostic, m.lastDiagnosticReason = time.Now(), reason
	_, _ = fmt.Fprintf(m.stdout, `{"level":"warn","message":"Runtime file logging degraded","module":"logging","component":"manager","reason":%q}`+"\n", reason)
}
func parseLevel(level string) zerolog.Level {
	parsed, err := zerolog.ParseLevel(level)
	if err != nil {
		return zerolog.InfoLevel
	}
	return parsed
}

func eventLevel(line []byte) zerolog.Level {
	var envelope struct {
		Level string `json:"level"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return zerolog.WarnLevel
	}
	return parseLevel(envelope.Level)
}
