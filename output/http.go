package output

type Http struct {
	Url    string `yaml:"url"`
	Secret string `yaml:"secret,omitempty"`
}
