package input

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

func TestReadServerSample(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"loadavg": "4.00 2.50 1.25 2/100 1000\n",
		"stat":    "cpu 1 2 3 4\ncpu0 1 2 3 4\ncpu1 1 2 3 4\nintr 0\n",
		"meminfo": "MemTotal: 1000000 kB\nMemFree: 10000 kB\nMemAvailable: 400000 kB\nCached: 390000 kB\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sample, err := readServerSample(dir)
	if err != nil {
		t.Fatal(err)
	}
	if sample.CPUs != 2 || sample.Load[0]/float64(sample.CPUs) != 2 || sample.memoryPercent() != 60 {
		t.Fatal("incorrect CPU normalization or available-memory calculation")
	}
	for _, test := range []struct{ name, file, data string }{
		{"missing_available", "meminfo", "MemTotal: 100 kB\nMemFree: 1 kB\n"},
		{"impossible_memory", "meminfo", "MemTotal: 100 kB\nMemAvailable: 101 kB\n"},
		{"wrong_unit", "meminfo", "MemTotal: 100 MB\nMemAvailable: 10 MB\n"},
		{"nan_load", "loadavg", "NaN 0 0"},
		{"short_load", "loadavg", "1"},
		{"missing_cpus", "stat", "cpu 0 1 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, test.file), []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readServerSample(dir); err == nil {
				t.Fatal("invalid sample accepted")
			}
			if err := os.WriteFile(filepath.Join(dir, test.file), []byte(files[test.file]), 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServerSustainCooldownAndRestart(t *testing.T) {
	store, err := newCursorStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sample := serverSample{Load: [3]float64{4, 2, 1}, CPUs: 4, TotalKiB: 1000, AvailableKiB: 100}
	var reports []publisher.Report
	monitor := serverMonitor{config: ServerConfig{Enabled: true, LoadPerCPU: 1, MemoryPercent: 90}, store: store, host: "test-host",
		read: func() (serverSample, error) { return sample, nil },
		send: func(_ context.Context, r publisher.Report) error { reports = append(reports, r); return nil },
	}
	for n := 0; n < 6; n++ {
		if err := monitor.poll(context.Background(), now.Add(time.Duration(n)*serverInterval)); err != nil {
			t.Fatal(err)
		}
	}
	if len(reports) != 0 {
		t.Fatal("transient load triggered early")
	}
	if err := monitor.poll(context.Background(), now.Add(serverHold)); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Kind != "server" || len(reports[0].Keywords) != 2 {
		t.Fatal("sustained host metrics not reported")
	}
	if strings.Contains(reports[0].Header(), "Project:") || strings.Contains(reports[0].Header(), "File:") {
		t.Fatal("server report fabricates log source")
	}
	firstID := reports[0].ID()
	store, err = newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	monitor.store = store
	for n := 7; n < 36; n++ {
		if err := monitor.poll(context.Background(), now.Add(time.Duration(n)*serverInterval)); err != nil {
			t.Fatal(err)
		}
	}
	if len(reports) != 1 {
		t.Fatal("cooldown was lost on restart")
	}
	if err := monitor.poll(context.Background(), now.Add(serverHold+serverCooldown)); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || reports[1].ID() == firstID {
		t.Fatal("continued high load did not produce a distinct reminder")
	}
	sample.Load[0] = 0
	sample.AvailableKiB = 500
	if err := monitor.poll(context.Background(), now.Add(37*serverInterval)); err != nil {
		t.Fatal(err)
	}
	state := store.data[serverKey].Server
	if !state.Load.Since.IsZero() || !state.Memory.Since.IsZero() {
		t.Fatal("normal reading did not reset hold")
	}
}

func TestServerFailureAndGaps(t *testing.T) {
	store, err := newCursorStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	readErr := false
	sample := serverSample{Load: [3]float64{2}, CPUs: 1, TotalKiB: 1000, AvailableKiB: 800}
	monitor := serverMonitor{config: ServerConfig{Enabled: true, LoadPerCPU: 1, MemoryPercent: 90}, store: store, host: "test-host",
		read: func() (serverSample, error) {
			if readErr {
				return serverSample{}, errors.New("missing proc data")
			}
			return sample, nil
		},
		send: func(context.Context, publisher.Report) error { return errors.New("offline") },
	}
	for n := 0; n < 3; n++ {
		if err := monitor.poll(context.Background(), now.Add(time.Duration(n)*serverInterval)); err != nil {
			t.Fatal(err)
		}
	}
	readErr = true
	if err := monitor.poll(context.Background(), now.Add(3*serverInterval)); err != nil {
		t.Fatal("sample failure stopped log monitoring")
	}
	readErr = false
	for n := 4; n < 10; n++ {
		if err := monitor.poll(context.Background(), now.Add(time.Duration(n)*serverInterval)); err != nil {
			t.Fatal("error gap counted as sustained high load")
		}
	}
	if err := monitor.poll(context.Background(), now.Add(10*serverInterval)); err == nil {
		t.Fatal("delivery failure swallowed")
	}
	restored, err := newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	c := restored.data[serverKey]
	if c.Pending == nil || c.Pending.Kind != "server" || len(c.Pending.Keywords) != 1 || c.Pending.Keywords[0] != "server_load" {
		t.Fatal("server retry not persisted or metrics not independent")
	}
	count := 0
	if err := restored.replay(context.Background(), func(_ context.Context, r publisher.Report) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("host report did not replay")
	}
	state := restored.data[serverKey].Server
	state.observe(monitor.config, sample, now.Add(time.Hour))
	if state.Load.Since != now.Add(time.Hour) {
		t.Fatal("unsampled gap treated as continuous overload")
	}
}

func TestServerConfigValidation(t *testing.T) {
	for _, config := range []ServerConfig{{LoadPerCPU: 0, MemoryPercent: 90}, {LoadPerCPU: math.NaN(), MemoryPercent: 90}, {LoadPerCPU: 1, MemoryPercent: math.Inf(1)}, {LoadPerCPU: 1, MemoryPercent: 101}} {
		if err := config.Validate(); err == nil {
			t.Fatal("invalid threshold accepted")
		}
	}
}

func TestServerDelayedReplayRestartsCooldown(t *testing.T) {
	store, err := newCursorStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-time.Hour)
	store.data[serverKey] = cursor{Identity: "server", Seen: old,
		Server:  &serverState{Load: serverAlarm{Since: old, LastSent: old}},
		Pending: &publisher.Report{Kind: "server", Identity: "server", Host: "test-host", At: old, Keywords: []string{"server_load"}},
	}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := store.replay(context.Background(), func(context.Context, publisher.Report) error { return nil }); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	alarm := reloaded.data[serverKey].Server.Load
	if alarm.LastSent.Before(now) || alarm.due(true, now.Add(time.Minute)) {
		t.Fatal("delayed replay did not start a fresh cooldown")
	}
}
