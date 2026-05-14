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
	"strconv"
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

// lokiEventLine is stored in Loki: same fields as ntfy plus optional exporter-only fields.
type lokiEventLine struct {
	NtfyEvent
	FigureASCII string `json:"figure_ascii,omitempty"`
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
	figureLineWidth := envFigureLineWidth()

	pusher := newLokiPusher(lokiURL, job)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for _, topic := range topics {
		topic := topic
		wg.Add(1)
		go func() {
			defer wg.Done()
			runTopicLoop(ctx, pusher, baseURL, topic, exportAll, printTitleFigure, figureLineWidth)
		}()
	}
	wg.Wait()
}

func runTopicLoop(ctx context.Context, p *lokiPusher, baseURL, topic string, exportAll, printTitleFigure bool, figureLineWidth int) {
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
			var figureASCII string
			if printTitleFigure && ev.Event == "message" {
				if phrase := figurePhrase(ev, figureLineWidth); phrase != "" {
					figureASCII = renderASCII(logger, phrase, figureLineWidth)
					if figureASCII != "" {
						fmt.Println(figureASCII)
						fmt.Println()
					}
				}
			}
			line, err := json.Marshal(lokiEventLine{NtfyEvent: ev, FigureASCII: figureASCII})
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

func envFigureLineWidth() int {
	s := strings.TrimSpace(os.Getenv("NTFY_FIGURE_LINE_WIDTH"))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

const maxFigurePhraseRunes = 80
const maxFigurePhraseSafetyRunes = 4096

// figurePhrase prefers the notification title; if empty, uses the first line of the message.
// With lineWidth<=0, caps at maxFigurePhraseRunes. With lineWidth>0, only a safety cap applies.
func figurePhrase(ev NtfyEvent, lineWidth int) string {
	var raw string
	if t := strings.TrimSpace(ev.Title); t != "" {
		raw = t
	} else {
		raw = firstLine(ev.Message)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = truncateRunes(raw, maxFigurePhraseSafetyRunes)
	if lineWidth <= 0 {
		return truncateRunes(raw, maxFigurePhraseRunes)
	}
	return normalizeLongWords(raw, lineWidth)
}

func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return ""
	}
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

// normalizeLongWords splits whitespace-separated tokens longer than maxRunes into chunks (blocklet + go-figure need this).
func normalizeLongWords(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(' ')
		}
		chunks := splitRunesMax(f, maxRunes)
		for j, c := range chunks {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(c)
		}
	}
	return b.String()
}

func splitRunesMax(s string, max int) []string {
	r := []rune(s)
	if len(r) <= max {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(r); i += max {
		j := i + max
		if j > len(r) {
			j = len(r)
		}
		out = append(out, string(r[i:j]))
	}
	return out
}

// logicalLines wraps words so each line is at most maxRunes runes (including spaces between words).
func logicalLines(s string, maxRunes int) []string {
	if maxRunes <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var cur []string
	curLen := 0
	flush := func() {
		if len(cur) > 0 {
			lines = append(lines, strings.Join(cur, " "))
			cur = nil
			curLen = 0
		}
	}
	for _, w := range words {
		for _, chunk := range splitRunesMax(w, maxRunes) {
			wl := utf8.RuneCountInString(chunk)
			sep := 0
			if len(cur) > 0 {
				sep = 1
			}
			if len(cur) > 0 && curLen+sep+wl > maxRunes {
				flush()
			}
			cur = append(cur, chunk)
			curLen += sep + wl
		}
	}
	flush()
	return lines
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

func renderASCII(logger *slog.Logger, phrase string, lineWidth int) string {
	if lineWidth <= 0 {
		return renderGoFigure(logger, phrase)
	}
	return renderGoFigureWrapped(logger, logicalLines(phrase, lineWidth))
}

func renderGoFigureWrapped(logger *slog.Logger, lines []string) string {
	var parts []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if fig := renderGoFigure(logger, ln); fig != "" {
			parts = append(parts, fig)
		}
	}
	return strings.Join(parts, "\n\n")
}


func renderGoFigure(logger *slog.Logger, phrase string) string {
	try := func() (string, error) {
		var art string
		var ferr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					ferr = fmt.Errorf("%v", r)
				}
			}()
			fig := figure.NewFigure(phrase, "", false)
			art = strings.TrimRight(fig.String(), "\n")
		}()
		if ferr != nil {
			return "", ferr
		}
		return art, nil
	}
	art, err := try()
	if err != nil {
		logger.Error("go-figure render failed", "err", err)
		return ""
	}
	return art
}
