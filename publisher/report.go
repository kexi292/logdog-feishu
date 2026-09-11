package publisher

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

type Report struct {
	Project, Service, Rule, Directory, File, Host, Identity string
	Start, End, Trigger                                     int64
	Keywords                                                []string
	Lines                                                   []string
	Completeness                                            string
	At                                                      time.Time
}

func (r Report) ID() string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", r.Project, r.Service, r.File, r.Identity, r.Trigger)))
	return fmt.Sprintf("%x", hash[:8])
}

func (r Report) Header() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Report: %s\nProject: %s\nService: %s\nHost: %s\nRule: %s\nDirectory: %s\nFile: %s\nFile identity: %s\nBytes: [%d, %d)\nTime: %s\nKeywords: %s\nContext: %s\n", r.ID(), r.Project, r.Service, r.Host, r.Rule, r.Directory, r.File, r.Identity, r.Start, r.End, r.At.Format(time.RFC3339), strings.Join(r.Keywords, ", "), r.Completeness)
	return b.String()
}

func (r Report) Text() string { return r.Header() + "\n" + strings.Join(r.Lines, "") }
