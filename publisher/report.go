package publisher

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

type Report struct {
	Kind                                                    string `json:",omitempty"`
	Project, Service, Rule, Directory, File, Host, Identity string
	Start, End, Trigger                                     int64
	Keywords                                                []string
	Lines                                                   []string
	Completeness                                            string
	At                                                      time.Time
}

func (r Report) ID() string {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", r.Project, r.Service, r.File, r.Identity, r.Trigger)
	if r.Kind == "server" {
		key = fmt.Sprintf("server\x00%s\x00%d\x00%s", r.Host, r.At.UnixNano(), strings.Join(r.Keywords, ","))
	}
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", hash[:8])
}

func (r Report) Type() string {
	if r.Kind == "" {
		return "log"
	}
	return r.Kind
}

func (r Report) Header() string {
	var b strings.Builder
	if r.Kind == "server" {
		fmt.Fprintf(&b, "Report: %s\nType: server health\nHost: %s\nTime: %s\nAlerts: %s\n", r.ID(), r.Host, r.At.Format(time.RFC3339), strings.Join(r.Keywords, ", "))
		return b.String()
	}
	fmt.Fprintf(&b, "Report: %s\nProject: %s\nService: %s\nHost: %s\nRule: %s\nDirectory: %s\nFile: %s\nFile identity: %s\nBytes: [%d, %d)\nTime: %s\nKeywords: %s\nContext: %s\n", r.ID(), r.Project, r.Service, r.Host, r.Rule, r.Directory, r.File, r.Identity, r.Start, r.End, r.At.Format(time.RFC3339), strings.Join(r.Keywords, ", "), r.Completeness)
	return b.String()
}

func (r Report) Text() string { return r.Header() + "\n" + strings.Join(r.Lines, "") }
