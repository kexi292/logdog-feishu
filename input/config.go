package input

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kexi292/logdog-feishu/output"
	"gopkg.in/yaml.v3"
)

type Inputs struct {
	Project       string   `yaml:"project"`
	Name          string   `yaml:"name"`
	ScanFrequency int64    `yaml:"scan_frequency"`
	Paths         []string `yaml:"paths"`
	IncludeLines  []string `yaml:"include_lines"`
	ExcludeLines  []string `yaml:"exclude_lines"`
}

type Config struct {
	ServerMonitor *ServerConfig `yaml:"server_monitor,omitempty"`
	StateFile     string        `yaml:"state_file,omitempty"`
	Inputs        []*Inputs     `yaml:"inputs"`
	OutputHttp    *output.Http  `yaml:"output.http"`
}

func (c *Config) Validate() error {
	if c == nil || c.OutputHttp == nil || strings.TrimSpace(c.OutputHttp.Url) == "" {
		return fmt.Errorf("output.http.url is required")
	}
	u, err := url.Parse(c.OutputHttp.Url)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("output.http.url must be a valid https URL")
	}
	if len(c.OutputHttp.Url) > 2048 || len(c.OutputHttp.Secret) > 1024 {
		return fmt.Errorf("webhook credentials exceed size limit")
	}
	if c.ServerMonitor != nil {
		if err := c.ServerMonitor.Validate(); err != nil {
			return err
		}
	}
	if len(c.Inputs) == 0 {
		return fmt.Errorf("at least one input is required")
	}
	seen := make(map[string]bool, len(c.Inputs))
	for n, in := range c.Inputs {
		if in == nil {
			return fmt.Errorf("input %d is empty", n+1)
		}
		if strings.TrimSpace(in.Project) == "" || strings.TrimSpace(in.Name) == "" {
			return fmt.Errorf("input %d requires project and service name", n+1)
		}
		if len(in.Project) > 128 || len(in.Name) > 128 || strings.ContainsAny(in.Project+in.Name, "\r\n\x00") {
			return fmt.Errorf("input %d has invalid project or service name", n+1)
		}
		if in.ScanFrequency < 0 || in.ScanFrequency > 3600 {
			return fmt.Errorf("input %d scan frequency must be 0 (default) or 1..3600 seconds", n+1)
		}
		key := strings.TrimSpace(in.Project) + "\x00" + strings.TrimSpace(in.Name)
		if seen[key] {
			return fmt.Errorf("input %d duplicates project and service name", n+1)
		}
		seen[key] = true
		if len(in.Paths) == 0 {
			return fmt.Errorf("input %d requires at least one path", n+1)
		}
		if len(in.IncludeLines) == 0 {
			return fmt.Errorf("input %d requires at least one include keyword", n+1)
		}
		for _, p := range in.Paths {
			if strings.TrimSpace(p) == "" || len(p) > 4096 || strings.ContainsAny(p, "\x00\r\n") {
				return fmt.Errorf("input %d contains an empty path", n+1)
			}
			if _, err := filepath.Match(p, ""); err != nil {
				return fmt.Errorf("input %d contains an invalid glob", n+1)
			}
		}
		for _, word := range append(append([]string(nil), in.IncludeLines...), in.ExcludeLines...) {
			if strings.TrimSpace(word) == "" || len(word) > 256 || strings.ContainsAny(word, "\x00\r\n") {
				return fmt.Errorf("input %d contains an invalid keyword", n+1)
			}
		}
		if len(in.IncludeLines) > 64 || len(in.ExcludeLines) > 64 {
			return fmt.Errorf("input %d supports at most 64 keywords per list", n+1)
		}
	}
	return nil
}

func Load(filename string) (*Config, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("configuration must be a regular file accessible only to its owner; use chmod 600")
	}
	b, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1024*1024 {
		return nil, fmt.Errorf("configuration exceeds 1 MiB")
	}
	c := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err := decoder.Decode(c); err != nil {
		return nil, fmt.Errorf("cannot parse configuration; check field names and YAML structure")
	}
	if err := decoder.Decode(&Config{}); err != io.EOF {
		return nil, fmt.Errorf("configuration must contain exactly one YAML document")
	}
	return c, nil
}

func Save(filename string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(filename)
	f, err := os.CreateTemp(dir, ".logdog-config-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, filename)
}
