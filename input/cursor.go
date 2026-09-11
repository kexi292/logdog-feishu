package input

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

type cursor struct {
	Identity string            `json:"identity"`
	Offset   int64             `json:"offset"`
	Seen     time.Time         `json:"seen"`
	Pending  *publisher.Report `json:"pending,omitempty"`
}

type cursorStore struct {
	path string
	data map[string]cursor
}

func newCursorStore(path string) (*cursorStore, error) {
	s := &cursorStore{path: path, data: map[string]cursor{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cursor state: %w", err)
	}
	if json.Unmarshal(b, &s.data) != nil || s.data == nil {
		return nil, errors.New("invalid cursor state; restore the state file before restarting")
	}
	for _, c := range s.data {
		if c.Offset < 0 || c.Identity == "" {
			return nil, errors.New("invalid cursor checkpoint")
		}
		if r := c.Pending; r != nil && (r.Start < 0 || r.End < r.Start || r.Identity != c.Identity) {
			return nil, errors.New("invalid pending report in cursor state")
		}
	}
	return s, nil
}

func cursorKey(project, service, path string) string {
	b, _ := json.Marshal([]string{project, service, path})
	return string(b)
}

func (s *cursorStore) offset(key, id string, size int64, initial bool) int64 {
	c, ok := s.data[key]
	if !ok {
		if initial {
			return size
		}
		return 0
	}
	if c.Identity != id || size < c.Offset {
		return 0
	}
	return c.Offset
}

func (s *cursorStore) set(key, id string, offset int64, now time.Time) {
	c := s.data[key]
	c.Identity, c.Offset, c.Seen = id, offset, now
	s.data[key] = c
}

// Persist the bounded report before HTTP so rotation cannot erase a failed delivery.
func (s *cursorStore) deliver(ctx context.Context, key string, send func(context.Context, publisher.Report) error) error {
	c := s.data[key]
	if c.Pending == nil {
		return nil
	}
	if err := s.save(); err != nil {
		return fmt.Errorf("persist pending report: %w", err)
	}
	if err := send(ctx, *c.Pending); err != nil {
		return err
	}
	c.Offset = c.Pending.End
	c.Pending = nil
	c.Seen = time.Now()
	s.data[key] = c
	if err := s.save(); err != nil {
		return fmt.Errorf("persist delivered report: %w", err)
	}
	return nil
}

func (s *cursorStore) replay(ctx context.Context, send func(context.Context, publisher.Report) error) error {
	keys := make([]string, 0, len(s.data))
	for key, c := range s.data {
		if c.Pending != nil {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := s.deliver(ctx, key, send); err != nil {
			return err
		}
	}
	return nil
}

func (s *cursorStore) save() error {
	for key, c := range s.data {
		if c.Pending == nil && time.Since(c.Seen) > 7*24*time.Hour {
			delete(s.data, key)
		}
	}
	b, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".logdog-state-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func identity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
