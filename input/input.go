package input

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

type trackedFile struct {
	path, directory, rule, key, identity string
	file                                 *File
	before                               []logLine
	event                                *eventReport
}

type Input struct {
	config   *Inputs
	files    map[string]*trackedFile
	cursors  *cursorStore
	host     string
	nextScan time.Time
	started  bool
	send     func(context.Context, publisher.Report) error
}

func newInput(config *Inputs, store *cursorStore, host string, send func(context.Context, publisher.Report) error) *Input {
	return &Input{config: config, cursors: store, host: host, send: send, files: map[string]*trackedFile{}}
}

func (i *Input) open(path, rule string, initial, reset bool) error {
	// Avoid blocking on FIFOs or devices that happen to match a log glob.
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open log %s: %w", path, err)
	}
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil
	}
	id := identity(info)
	key := cursorKey(i.config.Project, i.config.Name, path)
	offset := i.cursors.offset(key, id, info.Size(), initial)
	if reset {
		offset = 0
	}
	file, err := newFile(f, offset)
	if err != nil {
		f.Close()
		return err
	}
	before, err := file.history()
	if err != nil {
		f.Close()
		return fmt.Errorf("read preceding context %s: %w", path, err)
	}
	t := &trackedFile{path: path, directory: filepath.Dir(path), rule: rule, key: key, identity: id, file: file, before: before}
	i.files[path] = t
	i.checkpoint(t, time.Now())
	return nil
}

func (i *Input) scan(now time.Time) error {
	for _, rule := range i.config.Paths {
		paths, err := filepath.Glob(rule)
		if err != nil {
			return fmt.Errorf("invalid log path rule: %w", err)
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			candidates := []string{path}
			if info.IsDir() {
				candidates, err = filepath.Glob(filepath.Join(path, "*.log"))
				if err != nil {
					return err
				}
			}
			for _, candidate := range candidates {
				absolute, err := filepath.Abs(candidate)
				if err != nil {
					return err
				}
				if _, ok := i.files[absolute]; !ok {
					if err := i.open(absolute, rule, !i.started, false); err != nil {
						return err
					}
				}
			}
		}
	}
	i.started = true
	freq := i.config.ScanFrequency
	if freq == 0 {
		freq = 10
	}
	i.nextScan = now.Add(time.Duration(freq) * time.Second)
	return nil
}

func (i *Input) finishFile(ctx context.Context, t *trackedFile, reason string) error {
	if line := t.file.take(true); line != nil {
		if err := i.consume(ctx, t, *line, time.Now()); err != nil {
			return err
		}
	}
	if err := i.emit(ctx, t, reason); err != nil {
		return err
	}
	i.checkpoint(t, time.Now())
	if err := t.file.file.Close(); err != nil {
		return err
	}
	delete(i.files, t.path)
	return nil
}

func (i *Input) pollFile(ctx context.Context, t *trackedFile, now time.Time) error {
	info, err := t.file.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < t.file.offset {
		if err := i.finishFile(ctx, t, "incomplete: file truncated"); err != nil {
			return err
		}
		return i.open(t.path, t.rule, false, true)
	}
	eof := false
	// A busy file yields to the other services after bounded work.
	for n := 0; n < 256; n++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := t.file.read(now)
		if err == io.EOF {
			eof = true
			break
		}
		if err != nil {
			return fmt.Errorf("read log %s: %w", t.path, err)
		}
		if line != nil {
			if err := i.consume(ctx, t, *line, now); err != nil {
				return err
			}
		}
		i.checkpoint(t, now)
	}
	if eof {
		current, err := os.Stat(t.path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if os.IsNotExist(err) || identity(current) != t.identity {
			if err := i.finishFile(ctx, t, "incomplete: file removed or rotated"); err != nil {
				return err
			}
			if current != nil {
				return i.open(t.path, t.rule, false, true)
			}
			return nil
		}
		if t.file.start < t.file.offset && now.Sub(t.file.last) >= contextIdle {
			if line := t.file.take(true); line != nil {
				if err := i.consume(ctx, t, *line, now); err != nil {
					return err
				}
			}
		}
	}
	if t.event != nil {
		if now.Sub(t.event.report.At) >= contextDeadline {
			if err := i.emit(ctx, t, "incomplete: 10 second collection deadline"); err != nil {
				return err
			}
		} else if eof && now.Sub(t.event.last) >= contextIdle {
			if err := i.emit(ctx, t, "unknown: log stopped before a confirmed event boundary"); err != nil {
				return err
			}
		}
	}
	i.checkpoint(t, now)
	return nil
}

func (i *Input) poll(ctx context.Context, now time.Time) error {
	if !now.Before(i.nextScan) {
		if err := i.scan(now); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(i.files))
	for path := range i.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := i.pollFile(ctx, i.files[path], now); err != nil {
			return err
		}
	}
	return nil
}

func (i *Input) close(ctx context.Context, flush bool) error {
	var first error
	for _, t := range i.files {
		if flush {
			if err := i.finishFile(ctx, t, "unknown: process shutting down"); err != nil {
				if first == nil {
					first = err
				}
				_ = t.file.file.Close()
			}
		} else {
			_ = t.file.file.Close()
		}
	}
	return first
}

func (i *Input) announce() {
	log.Printf("watching project=%s service=%s files=%d", i.config.Project, i.config.Name, len(i.files))
}
