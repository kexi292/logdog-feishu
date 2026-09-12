package input

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

const (
	serverInterval = 30 * time.Second
	serverHold     = 3 * time.Minute
	serverCooldown = 15 * time.Minute
	serverKey      = "server-monitor"
)

type ServerConfig struct {
	Enabled       bool    `yaml:"enabled"`
	LoadPerCPU    float64 `yaml:"load_per_cpu"`
	MemoryPercent float64 `yaml:"memory_percent"`
}

func (c ServerConfig) Validate() error {
	if math.IsNaN(c.LoadPerCPU) || math.IsInf(c.LoadPerCPU, 0) || c.LoadPerCPU <= 0 || c.LoadPerCPU > 100 {
		return fmt.Errorf("server load threshold must be greater than 0 and at most 100 per CPU")
	}
	if math.IsNaN(c.MemoryPercent) || math.IsInf(c.MemoryPercent, 0) || c.MemoryPercent <= 0 || c.MemoryPercent > 100 {
		return fmt.Errorf("server memory threshold must be greater than 0 and at most 100 percent")
	}
	return nil
}

type serverSample struct {
	Load                   [3]float64
	CPUs                   int
	TotalKiB, AvailableKiB uint64
}

func (s serverSample) memoryPercent() float64 {
	return float64(s.TotalKiB-s.AvailableKiB) * 100 / float64(s.TotalKiB)
}

// /proc reflects the whole Linux host; container quotas are intentionally not inferred.
func readServerSample(root string) (serverSample, error) {
	var sample serverSample
	load, err := os.ReadFile(filepath.Join(root, "loadavg"))
	if err != nil {
		return sample, err
	}
	values := strings.Fields(string(load))
	if len(values) < 3 {
		return sample, fmt.Errorf("invalid loadavg data")
	}
	for n := range sample.Load {
		v, err := strconv.ParseFloat(values[n], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return sample, fmt.Errorf("invalid loadavg value")
		}
		sample.Load[n] = v
	}
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return sample, err
	}
	for _, line := range strings.Split(string(stat), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(fields[0], "cpu")); err == nil && n >= 0 {
			sample.CPUs++
		}
	}
	if sample.CPUs == 0 {
		return sample, fmt.Errorf("no CPU entries in proc stat")
	}
	mem, err := os.ReadFile(filepath.Join(root, "meminfo"))
	if err != nil {
		return sample, err
	}
	found := map[string]uint64{}
	for _, line := range strings.Split(string(mem), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return sample, fmt.Errorf("invalid meminfo units")
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return sample, fmt.Errorf("invalid meminfo value")
		}
		found[fields[0]] = value
	}
	total, ok := found["MemTotal:"]
	available, haveAvailable := found["MemAvailable:"]
	if !ok || !haveAvailable || total == 0 || available > total {
		return sample, fmt.Errorf("meminfo requires valid MemTotal and MemAvailable")
	}
	sample.TotalKiB, sample.AvailableKiB = total, available
	return sample, nil
}

type serverAlarm struct {
	Since    time.Time
	LastSent time.Time
}

func (a *serverAlarm) due(high bool, now time.Time) bool {
	if !high {
		a.Since = time.Time{}
		return false
	}
	if a.Since.IsZero() {
		a.Since = now
	}
	return now.Sub(a.Since) >= serverHold && (a.LastSent.IsZero() || now.Sub(a.LastSent) >= serverCooldown)
}

type serverState struct {
	Load, Memory                   serverAlarm
	LastCheck                      time.Time
	LoadThreshold, MemoryThreshold float64
}

func (s *serverState) observe(config ServerConfig, sample serverSample, now time.Time) []string {
	if now.Before(s.LastCheck) || now.Sub(s.LastCheck) > 2*serverInterval || s.LoadThreshold != config.LoadPerCPU || s.MemoryThreshold != config.MemoryPercent {
		s.Load.Since = time.Time{}
		s.Memory.Since = time.Time{}
	}
	s.LastCheck = now
	s.LoadThreshold, s.MemoryThreshold = config.LoadPerCPU, config.MemoryPercent
	var due []string
	if s.Load.due(sample.Load[0]/float64(sample.CPUs) >= config.LoadPerCPU, now) {
		due = append(due, "server_load")
	}
	if s.Memory.due(sample.memoryPercent() >= config.MemoryPercent, now) {
		due = append(due, "server_memory")
	}
	return due
}

type serverMonitor struct {
	config            ServerConfig
	store             *cursorStore
	host              string
	next, errorLogged time.Time
	read              func() (serverSample, error)
	send              func(context.Context, publisher.Report) error
}

func (m *serverMonitor) poll(ctx context.Context, now time.Time) error {
	if now.Before(m.next) {
		return nil
	}
	m.next = now.Add(serverInterval)
	c := m.store.data[serverKey]
	if c.Server == nil {
		c.Server = &serverState{}
	}
	c.Identity, c.Seen = "server", now
	sample, err := m.read()
	if err != nil {
		c.Server.Load.Since = time.Time{}
		c.Server.Memory.Since = time.Time{}
		m.store.data[serverKey] = c
		if m.errorLogged.IsZero() || now.Sub(m.errorLogged) >= serverCooldown {
			log.Printf("event=server_sample_failed error=%q", err.Error())
			m.errorLogged = now
		}
		return nil
	}
	if !m.errorLogged.IsZero() {
		log.Printf("event=server_sample_recovered")
		m.errorLogged = time.Time{}
	}
	due := c.Server.observe(m.config, sample, now)
	if len(due) == 0 {
		m.store.data[serverKey] = c
		return nil
	}
	for _, kind := range due {
		if kind == "server_load" {
			c.Server.Load.LastSent = now
		} else {
			c.Server.Memory.LastSent = now
		}
	}
	text := fmt.Sprintf("服务器持续高负载（连续采样至少 3 分钟）\n平均负载 1/5/15 分钟：%.2f / %.2f / %.2f\nCPU 核数：%d\n每核 1 分钟负载：%.2f，阈值：%.2f\n内存使用率：%.1f%%，阈值：%.1f%%\n内存总量 / 可用：%.0f / %.0f MiB\n平均负载包含 CPU 排队和不可中断等待，不等同于 CPU 使用率。\n", sample.Load[0], sample.Load[1], sample.Load[2], sample.CPUs, sample.Load[0]/float64(sample.CPUs), m.config.LoadPerCPU, sample.memoryPercent(), m.config.MemoryPercent, float64(sample.TotalKiB)/1024, float64(sample.AvailableKiB)/1024)
	c.Pending = &publisher.Report{Kind: "server", Identity: "server", Host: m.host, At: now, Keywords: due, Lines: []string{text}}
	m.store.data[serverKey] = c
	return m.store.deliver(ctx, serverKey, m.send)
}
