package input

import (
	"context"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

func Run(ctx context.Context, config *Config, statePath string, send func(context.Context, publisher.Report) error) (runErr error) {
	if err := config.Validate(); err != nil {
		return err
	}
	lock, err := os.OpenFile(statePath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open cursor lock: %w", err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("cursor state is already in use or cannot be locked: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	store, err := newCursorStore(statePath)
	if err != nil {
		return err
	}
	if err := store.replay(ctx, send); err != nil {
		return fmt.Errorf("replay pending report: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	var inputs []*Input
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, in := range inputs {
			if err := in.close(shutdown, ctx.Err() != nil); err != nil && runErr == nil {
				runErr = err
			}
		}
		if err := store.save(); err != nil {
			runErr = fmt.Errorf("save cursor state: %w (previous error: %v)", err, runErr)
		}
	}()
	for _, entry := range config.Inputs {
		in := newInput(entry, store, host, send)
		inputs = append(inputs, in)
		if err := in.scan(time.Now()); err != nil {
			return err
		}
		in.announce()
	}
	if err := store.save(); err != nil {
		return err
	}
	var server *serverMonitor
	if config.ServerMonitor != nil && config.ServerMonitor.Enabled {
		server = &serverMonitor{config: *config.ServerMonitor, store: store, host: host, send: send, read: func() (serverSample, error) { return readServerSample("/proc") }}
		log.Printf("event=server_monitor_started interval_seconds=30 hold_seconds=180 cooldown_seconds=900 load_per_cpu=%.2f memory_percent=%.1f", server.config.LoadPerCPU, server.config.MemoryPercent)
	}
	nextSave := time.Now().Add(10 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	// ponytail: synchronous delivery pauses scanning; decouple only if measured throughput needs it.
	for {
		if server != nil {
			if err := server.poll(ctx, time.Now()); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		for _, in := range inputs {
			if err := in.poll(ctx, time.Now()); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		if time.Now().After(nextSave) {
			if err := store.save(); err != nil {
				return err
			}
			nextSave = time.Now().Add(10 * time.Second)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
