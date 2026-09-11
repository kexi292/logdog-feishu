package input

import (
	"fmt"
	"github.com/zhjx922/alert/output"
	"gopkg.in/yaml.v3"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	Inputs     []*Inputs    `yaml:"inputs"`
	OutputHttp *output.Http `yaml:"output.http"`
}

func (c *Config) Validate() error {
	if c == nil || c.OutputHttp == nil || strings.TrimSpace(c.OutputHttp.Url) == "" {
		return fmt.Errorf("output.http.url is required")
	}
	u, err := url.Parse(c.OutputHttp.Url)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("output.http.url must be a valid https URL")
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
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("input %d contains an empty path", n+1)
			}
		}
	}
	return nil
}

func Load(filename string) (*Config, error) {
	b, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
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
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, filename)
}
