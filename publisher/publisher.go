package publisher

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kexi292/logdog-feishu/output"
)

const maxRequestBytes = 19 * 1024

// Publisher is used sequentially by the polling loop; retries share its quota.
type Publisher struct {
	config   *output.Http
	client   *http.Client
	next     time.Time
	interval time.Duration
}

func NewPublisher(config *output.Http) *Publisher {
	return &Publisher{config: config, interval: 650 * time.Millisecond, client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *Publisher) payload(text string, now time.Time) ([]byte, error) {
	body := struct {
		Type      string            `json:"msg_type"`
		Content   map[string]string `json:"content"`
		Timestamp string            `json:"timestamp,omitempty"`
		Sign      string            `json:"sign,omitempty"`
	}{Type: "text", Content: map[string]string{"text": text}}
	if p.config.Secret != "" {
		body.Timestamp = strconv.FormatInt(now.Unix(), 10)
		mac := hmac.New(sha256.New, []byte(body.Timestamp+"\n"+p.config.Secret))
		body.Sign = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	return json.Marshal(body)
}

func (p *Publisher) parts(r Report) ([]string, error) {
	text := []rune(strings.Join(r.Lines, ""))
	var parts []string
	for len(text) > 0 {
		lo, hi := 0, len(text)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			body, err := p.payload(r.Header()+"Part: 999/999\n\n"+string(text[:mid]), time.Now())
			if err != nil {
				return nil, err
			}
			if len(body) <= maxRequestBytes {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		if lo == 0 || len(parts) >= 999 {
			return nil, errors.New("report metadata or part count exceeds delivery limit")
		}
		parts = append(parts, string(text[:lo]))
		text = text[lo:]
	}
	if len(parts) == 0 {
		parts = append(parts, "")
	}
	for n := range parts {
		parts[n] = fmt.Sprintf("%sPart: %d/%d\n\n%s", r.Header(), n+1, len(parts), parts[n])
	}
	return parts, nil
}

func wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Publisher) Send(ctx context.Context, report Report) (sendErr error) {
	started := time.Now()
	id := report.ID()
	defer func() {
		if sendErr != nil {
			log.Printf("event=send_failed report_id=%s error=%q", id, sendErr.Error())
		}
	}()
	parts, err := p.parts(report)
	if err != nil {
		return err
	}
	log.Printf("event=send_started report_id=%s kind=%q project=%q service=%q host=%q file=%q parts=%d", id, report.Type(), report.Project, report.Service, report.Host, report.File, len(parts))
	for n, text := range parts {
		for attempt := 0; attempt < 3; attempt++ {
			if err := wait(ctx, time.Until(p.next)); err != nil {
				return err
			}
			p.next = time.Now().Add(p.interval)
			log.Printf("event=send_attempt report_id=%s part=%d/%d attempt=%d/3", id, n+1, len(parts), attempt+1)
			retry, err := p.sendPart(ctx, text)
			if err == nil {
				if len(parts) > 1 {
					log.Printf("event=part_sent report_id=%s part=%d/%d", id, n+1, len(parts))
				}
				break
			}
			if !retry || attempt == 2 {
				return fmt.Errorf("report %s part %d/%d: %w", report.ID(), n+1, len(parts), err)
			}
			log.Printf("event=send_retry report_id=%s part=%d/%d error=%q", id, n+1, len(parts), err.Error())
			if err := wait(ctx, time.Second<<attempt); err != nil {
				return err
			}
		}
	}
	log.Printf("event=send_succeeded report_id=%s kind=%q project=%q service=%q host=%q file=%q parts=%d duration_ms=%d", id, report.Type(), report.Project, report.Service, report.Host, report.File, len(parts), time.Since(started).Milliseconds())
	return nil
}

func (p *Publisher) sendPart(ctx context.Context, text string) (bool, error) {
	body, err := p.payload(text, time.Now())
	if err != nil {
		return false, err
	}
	if len(body) > maxRequestBytes {
		return false, errors.New("encoded message exceeds delivery limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.Url, bytes.NewReader(body))
	if err != nil {
		return false, errors.New("invalid webhook request")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// net/url errors contain the credential-bearing webhook URL.
		return true, errors.New("webhook connection failed (network or TLS)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode == 429 || resp.StatusCode >= 500, fmt.Errorf("webhook HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return true, errors.New("cannot read webhook response")
	}
	var result struct {
		Code *int `json:"code"`
	}
	if len(data) > 4096 || json.Unmarshal(data, &result) != nil || result.Code == nil {
		return false, errors.New("invalid webhook response")
	}
	if *result.Code != 0 {
		return *result.Code == 9499, fmt.Errorf("webhook business code %d", *result.Code)
	}
	return false, nil
}
