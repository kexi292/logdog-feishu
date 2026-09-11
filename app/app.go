package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kexi292/logdog-feishu/input"
	"github.com/kexi292/logdog-feishu/publisher"
)

func Run(ctx context.Context, configFile string) error {
	c, err := input.Load(configFile)
	if err != nil {
		return err
	}
	if err = c.Validate(); err != nil {
		return err
	}
	state := c.StateFile
	if state == "" {
		state = configFile + ".state"
	} else if !filepath.IsAbs(state) {
		state = filepath.Join(filepath.Dir(configFile), state)
	}
	configPath, err := filepath.Abs(configFile)
	if err != nil {
		return err
	}
	statePath, err := filepath.Abs(state)
	if err != nil {
		return err
	}
	configInfo, err := os.Stat(configPath)
	if err != nil {
		return err
	}
	stateInfo, stateErr := os.Stat(statePath)
	if configPath == statePath || stateErr == nil && os.SameFile(configInfo, stateInfo) {
		return fmt.Errorf("state_file must not overwrite the configuration")
	}
	return input.Run(ctx, c, statePath, publisher.NewPublisher(c.OutputHttp).Send)
}
