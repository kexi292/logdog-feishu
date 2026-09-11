package input

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

const (
	maxReportBytes  = 256 * 1024
	maxReportLines  = 1000
	contextIdle     = 2 * time.Second
	contextDeadline = 10 * time.Second
)

// ponytail: heuristic event boundaries; add format-specific parsers when logs need them.
var eventStart = regexp.MustCompile(`^(?:\[?\d{4}-\d{2}-\d{2}[ T]|\[?(?:TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\b)`)

type eventReport struct {
	report            publisher.Report
	bytes, tail       int
	boundary, damaged bool
	last              time.Time
}

func matching(text string, config *Inputs) []string {
	for _, word := range config.ExcludeLines {
		if strings.Contains(text, word) {
			return nil
		}
	}
	var words []string
	for _, word := range config.IncludeLines {
		if strings.Contains(text, word) {
			words = addWords(words, word)
		}
	}
	return words
}

func addWords(words []string, additions ...string) []string {
	for _, word := range additions {
		found := false
		for _, old := range words {
			if old == word {
				found = true
				break
			}
		}
		if !found {
			words = append(words, word)
		}
	}
	return words
}

func (i *Input) consume(ctx context.Context, t *trackedFile, line logLine, now time.Time) error {
	words := matching(line.Text, i.config)
	if t.event == nil && len(words) > 0 {
		r := publisher.Report{Project: i.config.Project, Service: i.config.Name, Host: i.host, Rule: t.rule,
			Directory: t.directory, File: t.path, Identity: t.identity, At: now, Start: line.Start, Trigger: line.Start}
		t.event = &eventReport{report: r, damaged: len(t.before) < 5}
		for _, old := range t.before {
			if old.Start < t.event.report.Start {
				t.event.report.Start = old.Start
			}
			t.event.report.Lines = append(t.event.report.Lines, old.Text)
			t.event.bytes += len(old.Text)
			t.event.damaged = t.event.damaged || old.Incomplete
		}
	}
	if e := t.event; e != nil {
		e.report.Lines = append(e.report.Lines, line.Text)
		e.report.End = line.End
		e.report.Keywords = addWords(e.report.Keywords, words...)
		e.bytes += len(line.Text)
		e.damaged = e.damaged || line.Incomplete
		e.last = now
		if len(words) > 0 {
			e.boundary = false
			e.tail = 0
		} else if eventStart.MatchString(line.Text) {
			e.boundary = true
		}
		if e.boundary {
			e.tail++
		}
		switch {
		case e.bytes >= maxReportBytes || len(e.report.Lines) >= maxReportLines:
			if err := i.emit(ctx, t, "incomplete: collection limit; remaining content is in the original file"); err != nil {
				return err
			}
		case e.tail >= 5:
			if err := i.emit(ctx, t, "boundary inferred; 5 following lines collected (request association unknown)"); err != nil {
				return err
			}
		}
	}
	t.before = append(t.before, line)
	if len(t.before) > 5 {
		t.before = t.before[1:]
	}
	return nil
}

func (i *Input) checkpoint(t *trackedFile, now time.Time) {
	offset := t.file.start
	if t.event != nil && t.event.report.Trigger < offset {
		offset = t.event.report.Trigger
	}
	i.cursors.set(t.key, t.identity, offset, now)
}

func (i *Input) emit(ctx context.Context, t *trackedFile, status string) error {
	if t.event == nil {
		return nil
	}
	i.checkpoint(t, time.Now())
	if t.event.damaged {
		status += "; incomplete: preceding context, partial line or line encoding/size limit"
	}
	t.event.report.Completeness = status
	c := i.cursors.data[t.key]
	if c.Pending == nil {
		report := t.event.report
		c.Pending = &report
		i.cursors.data[t.key] = c
	}
	if err := i.cursors.deliver(ctx, t.key, i.send); err != nil {
		return err
	}
	t.event = nil
	return nil
}
