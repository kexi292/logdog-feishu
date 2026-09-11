package app

import (
	"github.com/zhjx922/alert/input"
	"github.com/zhjx922/alert/publisher"
)

type Alert struct {
	Config *input.Config
	keeper *input.Keeper
}

func InitConfig(filename string) (*input.Config, error) {
	return input.Load(filename)
}

func NewAlert(configFile string) *Alert {
	c, err := InitConfig(configFile)

	if err != nil {
		panic(err)
	}

	return &Alert{Config: c}
}

func (a *Alert) Run() error {
	p := publisher.NewPublisher(a.Config.OutputHttp)
	go p.Monitor()

	k := input.NewKeeper()
	k.SetPublisher(p)
	k.Run(a.Config)

	return nil
}
