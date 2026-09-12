package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kexi292/logdog-feishu/output"
)

func TestReportPartsPreserveText(t *testing.T) {
	p := NewPublisher(&output.Http{Secret: "test-only"})
	signed, err := p.payload("example", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	var signature struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
	}
	if err := json.Unmarshal(signed, &signature); err != nil {
		t.Fatal(err)
	}
	if signature.Timestamp != "1700000000" || signature.Sign != "r1vXCpf/RWWqnp7i+VDfr462uSBkPLleHFEFsHCWnD0=" {
		t.Fatal("signature does not match independent test vector")
	}
	r := Report{Project: "demo", Service: "api", File: "/var/log/example.log", Directory: "/var/log", Identity: "1:2", At: time.Unix(1700000000, 0), Lines: []string{strings.Repeat("ERROR 中文 <tag> \"quote\" \\ \n", 3000)}}
	parts, err := p.parts(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 {
		t.Fatal("long report was not split")
	}
	var reconstructed strings.Builder
	for n, part := range parts {
		body, err := p.payload(part, time.Unix(1700000000, 0))
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > maxRequestBytes || !json.Valid(body) || !utf8.ValidString(part) {
			t.Fatal("invalid or oversized part")
		}
		var decoded struct {
			Type    string            `json:"msg_type"`
			Content map[string]string `json:"content"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		prefix := fmt.Sprintf("%sPart: %d/%d\n\n", r.Header(), n+1, len(parts))
		if decoded.Type != "text" || !strings.HasPrefix(decoded.Content["text"], prefix) {
			t.Fatal("missing source or part number")
		}
		reconstructed.WriteString(strings.TrimPrefix(decoded.Content["text"], prefix))
	}
	if reconstructed.String() != strings.Join(r.Lines, "") {
		t.Fatal("split changed original log context")
	}
	unsigned, err := NewPublisher(&output.Http{}).payload("example", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unsigned), "sign") || strings.Contains(string(unsigned), "timestamp") {
		t.Fatal("unsigned payload contains signing fields")
	}
}

func TestHTTPResponsesAndRetry(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        string
		retryThenOK bool
		wantCalls   int
		wantError   string
	}{
		{"success", 200, `{"code":0}`, false, 1, ""},
		{"business_failure", 200, `{"code":19021}`, false, 1, "19021"},
		{"missing_code", 200, `{}`, false, 1, "invalid webhook response"},
		{"invalid_json", 200, `{"code":0}garbage`, false, 1, "invalid webhook response"},
		{"permanent_http", 400, `{}`, false, 1, "400"},
		{"transient_http", 503, `{}`, true, 2, ""},
		{"rate_limit", 429, `{}`, false, 3, "429"},
		{"business_rate_limit", 200, `{"code":9499}`, true, 2, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					t.Error("invalid request method or content type")
				}
				if test.retryThenOK && calls > 1 {
					fmt.Fprint(w, `{"code":0}`)
					return
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			p := NewPublisher(&output.Http{Url: server.URL + "/private-placeholder", Secret: "test-signing-secret"})
			p.client.Transport = server.Client().Transport
			p.interval = 0
			err := p.Send(context.Background(), Report{Project: "demo", Service: "api", File: "/var/log/example.log", Lines: []string{"DO-NOT-LOG-THIS-BODY\n"}})
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("unexpected error: %v", err)
			}
			if calls != test.wantCalls {
				t.Fatalf("requests=%d, want %d", calls, test.wantCalls)
			}
			output := logs.String()
			for _, forbidden := range []string{server.URL, "private-placeholder", "test-signing-secret", "DO-NOT-LOG-THIS-BODY"} {
				if strings.Contains(output, forbidden) {
					t.Fatal("sensitive data leaked in delivery logs")
				}
			}
			if strings.Count(output, "event=send_attempt") != calls || !strings.Contains(output, "event=send_started") {
				t.Fatal("delivery attempt logs missing")
			}
			if test.wantError == "" {
				if strings.Count(output, "event=send_succeeded") != 1 || strings.Contains(output, "event=send_failed") {
					t.Fatal("successful delivery not logged accurately")
				}
			} else if !strings.Contains(output, "event=send_failed") || strings.Contains(output, "event=send_succeeded") {
				t.Fatal("failed delivery logged as success")
			}
			if calls > 1 && !strings.Contains(output, "event=send_retry") {
				t.Fatal("retry log missing")
			}
		})
	}
}

func TestTLSRedirectAndCancellation(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/unexpected", http.StatusFound) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	p := NewPublisher(&output.Http{Url: server.URL + "/private-placeholder"})
	_, err := p.sendPart(context.Background(), "test")
	if err == nil || strings.Contains(err.Error(), "private-placeholder") {
		t.Fatal("TLS validation bypassed or URL leaked")
	}
	p.client.Transport = server.Client().Transport
	retry, err := p.sendPart(context.Background(), "test")
	if err == nil || retry || !strings.Contains(err.Error(), "302") {
		t.Fatal("redirect followed or retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Send(ctx, Report{Lines: []string{"test"}}); err != context.Canceled {
		t.Fatalf("cancellation not propagated: %v", err)
	}
	p.next = time.Now().Add(time.Hour)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := p.Send(ctx, Report{Lines: []string{"test"}}); err != context.DeadlineExceeded {
		t.Fatalf("rate wait ignored context: %v", err)
	}
}
