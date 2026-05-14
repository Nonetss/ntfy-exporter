package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/common-nighthawk/go-figure"
)

// NtfyEvent matches lines from GET /{topic}/json (newline-delimited JSON).
type NtfyEvent struct {
	ID       string `json:"id"`
	Time     int64  `json:"time"`
	Event    string `json:"event"`
	Topic    string `json:"topic"`
	Title    string `json:"title,omitempty"`
	Message  string `json:"message"`
	Priority *int   `json:"priority,omitempty"`
	Expires  int64  `json:"expires,omitempty"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	baseURL := strings.TrimRight(
		firstNonEmpty(os.Getenv("NTFY_BASE_URL"), os.Getenv("NTFY_URL")),
		"/",
	)
	if baseURL == "" {
		log.Fatal("set NTFY_BASE_URL or NTFY_URL (e.g. https://ntfy.example.com)")
	}
	topics := parseCSV(os.Getenv("NTFY_TOPICS"))
	if len(topics) == 0 {
		log.Fatal("set NTFY_TOPICS (comma-separated topics, e.g. prueba,alerts)")
	}
	lokiURL := strings.TrimRight(os.Getenv("LOKI_URL"), "/")
	if lokiURL == "" {
		log.Fatal("set LOKI_URL (e.g. http://localhost:3100)")
	}

	job := os.Getenv("LOKI_JOB")
	if job == "" {
		job = "ntfy"
	}
	exportAll := envBool("NTFY_EXPORT_ALL_EVENTS")
	printTitleFigure := envBool("NTFY_PRINT_TITLE_FIGURE")
	figureFont := strings.TrimSpace(os.Getenv("NTFY_TITLE_FIGURE_FONT"))

	pusher := newLokiPusher(lokiURL, job)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for _, topic := range topics {
		topic := topic
		wg.Add(1)
		go func() {
			defer wg.Done()
			runTopicLoop(ctx, pusher, baseURL, topic, exportAll, printTitleFigure, figureFont)
		}()
	}
	wg.Wait()
}

func runTopicLoop(ctx context.Context, p *lokiPusher, baseURL, topic string, exportAll, printTitleFigure bool, figureFont string) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	streamURL := fmt.Sprintf("%s/%s/json", baseURL, topic)
	logger := slog.With("topic", topic)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := consumeStream(ctx, streamURL, func(ev NtfyEvent) error {
			if !exportAll && ev.Event != "message" {
				return nil
			}
			if printTitleFigure && ev.Event == "message" {
				if phrase := figurePhrase(ev); phrase != "" {
					figure.NewFigure(phrase, figureFont, false).Print()
					fmt.Println()
				}
			}
			line, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			ts := ev.eventTime()
			return p.push(ctx, ts, topic, ev.Event, ev.Priority, string(line))
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("stream ended, reconnecting", "err", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = time.Second
	}
}

func consumeStream(ctx context.Context, url string, handle func(NtfyEvent) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-ndjson, application/json;q=0.9, */*;q=0.8")

	resp, err := streamHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("ntfy %s: %s (body: %q)", url, resp.Status, body)
	}

	sc := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev NtfyEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			log.Printf("skip malformed json on topic stream %s: %v", url, err)
			continue
		}
		if err := handle(ev); err != nil {
			return fmt.Errorf("handler: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

func (ev *NtfyEvent) eventTime() time.Time {
	if ev.Time > 0 {
		return time.Unix(ev.Time, 0).UTC()
	}
	return time.Now().UTC()
}

type lokiPusher struct {
	base       string
	job        string
	httpClient *http.Client
	tenant     string
	user       string
	pass       string
}

func newLokiPusher(base, job string) *lokiPusher {
	return &lokiPusher{
		base: strings.TrimRight(base, "/"),
		job:  job,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		tenant: os.Getenv("LOKI_TENANT_ID"),
		user:   os.Getenv("LOKI_BASIC_AUTH_USER"),
		pass:   os.Getenv("LOKI_BASIC_AUTH_PASSWORD"),
	}
}

type lokiPushBody struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

func (p *lokiPusher) push(ctx context.Context, ts time.Time, topic, event string, priority *int, line string) error {
	ns := fmt.Sprintf("%d", ts.UnixNano())
	labels := map[string]string{
		"job":    p.job,
		"topic":  topic,
		"source": "ntfy",
		"event":  event,
	}
	if priority != nil {
		labels["priority"] = fmt.Sprintf("%d", *priority)
	}
	body := lokiPushBody{
		Streams: []lokiStream{
			{
				Stream: labels,
				Values: [][2]string{{ns, line}},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}

	endpoint := p.base + "/loki/api/v1/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.tenant != "" {
		req.Header.Set("X-Scope-OrgID", p.tenant)
	}
	if p.user != "" {
		req.SetBasicAuth(p.user, p.pass)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("loki push %s: %s body=%q", endpoint, resp.Status, b)
	}
	return nil
}

var streamHTTPClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	},
}

func parseCSV(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

const maxFigurePhraseRunes = 80

// figurePhrase prefers the notification title; if empty (common with curl -d "…"),
// uses the first line of the message body. Long text is truncated for sane terminal width.
func figurePhrase(ev NtfyEvent) string {
	if t := strings.TrimSpace(ev.Title); t != "" {
		return truncateFigurePhrase(t)
	}
	return truncateFigurePhrase(firstLine(ev.Message))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if i := strings.IndexByte(s, '\r'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func truncateFigurePhrase(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return ""
	}
	if len(r) <= maxFigurePhraseRunes {
		return string(r)
	}
	return string(r[:maxFigurePhraseRunes])
}
