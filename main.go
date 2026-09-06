package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

type config struct {
	appToken string
	botToken string
	axPath   string
	workdir  string
	sessions string
	model    string
	baseURL  string
	system   string
}

type socketOpen struct {
	OK    bool   `json:"ok"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type envelope struct {
	EnvelopeID   string          `json:"envelope_id"`
	Type         string          `json:"type"`
	AcceptsReply bool            `json:"accepts_response_payload"`
	Payload      json.RawMessage `json:"payload"`
}

type eventPayload struct {
	TeamID         string          `json:"team_id"`
	Event          slackEvent      `json:"event"`
	Authorizations []authorization `json:"authorizations"`
}

type slackEvent struct {
	Type        string `json:"type"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	Subtype     string `json:"subtype"`
	BotID       string `json:"bot_id"`
}

type authorization struct {
	UserID string `json:"user_id"`
}

type job struct {
	teamID   string
	channel  string
	threadTS string
	prompt   string
}

type slackResponse struct {
	OK    bool   `json:"ok"`
	TS    string `json:"ts"`
	Error string `json:"error"`
}

type axEvent struct {
	Type     string          `json:"type"`
	Message  json.RawMessage `json:"message"`
	Outcome  string          `json:"outcome"`
	Messages []axMessage     `json:"messages"`
}

type axMessage struct {
	Role      string          `json:"Role"`
	Content   string          `json:"Content"`
	ToolCalls json.RawMessage `json:"ToolCalls"`
}

var markdownLink = regexp.MustCompile(`\[([^]]+)\]\((https?://[^ )]+)\)`)
var markdownBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
var markdownUnderlineBold = regexp.MustCompile(`__([^_]+)__`)
var markdownStrike = regexp.MustCompile(`~~([^~]+)~~`)
var tableDelimiter = regexp.MustCompile(`^:?-{3,}:?$`)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	jobs := make(chan job, 64)
	seen := make(map[string]struct{})
	go work(cfg, jobs)
	for ctx.Err() == nil {
		if err := serve(ctx, cfg, jobs, seen); err != nil && ctx.Err() == nil {
			log.Printf("slack: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func loadConfig() (config, error) {
	cfg := config{
		appToken: os.Getenv("SLACK_APP_TOKEN"),
		botToken: os.Getenv("SLACK_BOT_TOKEN"),
		axPath:   os.Getenv("SLAXI_AX_PATH"),
		workdir:  os.Getenv("SLAXI_WORKDIR"),
		model:    os.Getenv("SLAXI_MODEL"),
		baseURL:  os.Getenv("SLAXI_BASE_URL"),
	}
	if cfg.appToken == "" {
		return cfg, errors.New("SLACK_APP_TOKEN is required")
	}
	if cfg.botToken == "" {
		return cfg, errors.New("SLACK_BOT_TOKEN is required")
	}
	if cfg.axPath == "" {
		cfg.axPath = "ax"
	}
	path, err := exec.LookPath(cfg.axPath)
	if err != nil {
		return cfg, fmt.Errorf("find ax: %w", err)
	}
	cfg.axPath = path
	if cfg.workdir == "" {
		workdir, err := os.Getwd()
		if err != nil {
			return cfg, fmt.Errorf("working directory: %w", err)
		}
		cfg.workdir = workdir
	}
	systemFile := os.Getenv("SLAXI_SYSTEM_FILE")
	if systemFile != "" {
		data, err := os.ReadFile(systemFile)
		if err != nil {
			return cfg, fmt.Errorf("read system prompt: %w", err)
		}
		cfg.system = strings.TrimSpace(string(data))
	}
	cfg.sessions = os.Getenv("SLAXI_SESSION_DIR")
	if cfg.sessions == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return cfg, fmt.Errorf("config directory: %w", err)
		}
		cfg.sessions = filepath.Join(root, "slaxi", "sessions")
	}
	return cfg, nil
}

func serve(ctx context.Context, cfg config, jobs chan<- job, seen map[string]struct{}) error {
	socketURL, err := openSocket(ctx, cfg)
	if err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, socketURL, nil)
	if err != nil {
		return fmt.Errorf("connect socket: %w", err)
	}
	defer conn.CloseNow()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read socket: %w", err)
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if env.EnvelopeID == "" {
			continue
		}
		ack, _ := json.Marshal(struct {
			EnvelopeID string `json:"envelope_id"`
		}{env.EnvelopeID})
		if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
			return fmt.Errorf("ack event: %w", err)
		}
		if _, ok := seen[env.EnvelopeID]; ok {
			continue
		}
		if len(seen) >= 10000 {
			clear(seen)
		}
		seen[env.EnvelopeID] = struct{}{}
		if env.Type != "events_api" {
			continue
		}
		j, ok := parseJob(env.Payload)
		if !ok {
			continue
		}
		select {
		case jobs <- j:
		default:
			log.Printf("drop event %s: queue full", env.EnvelopeID)
		}
	}
}

func openSocket(ctx context.Context, cfg config) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.appToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("open socket: %w", err)
	}
	defer resp.Body.Close()
	var result socketOpen
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode socket response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("open socket: %s", result.Error)
	}
	return result.URL, nil
}

func parseJob(data []byte) (job, bool) {
	var payload eventPayload
	if json.Unmarshal(data, &payload) != nil {
		return job{}, false
	}
	event := payload.Event
	isMention := event.Type == "app_mention"
	isDM := event.Type == "message" && event.ChannelType == "im" && event.Subtype == "" && event.BotID == ""
	if !isMention && !isDM {
		return job{}, false
	}
	threadTS := payload.Event.ThreadTS
	if threadTS == "" {
		threadTS = payload.Event.TS
	}
	if !safePart(payload.TeamID) || !safePart(payload.Event.Channel) || !safePart(threadTS) {
		return job{}, false
	}
	prompt := payload.Event.Text
	if len(payload.Authorizations) > 0 {
		prompt = strings.ReplaceAll(prompt, "<@"+payload.Authorizations[0].UserID+">", "")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return job{}, false
	}
	return job{payload.TeamID, payload.Event.Channel, threadTS, prompt}, true
}

func safePart(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func work(cfg config, jobs <-chan job) {
	for j := range jobs {
		messageTS, err := postMessage(cfg, j, "Working…")
		if err != nil {
			log.Printf("progress: %v", err)
		}
		reply, err := executeJob(cfg, j)
		if err != nil {
			log.Printf("ax: %v", err)
			reply = "AX failed: " + err.Error()
		}
		parts := splitMessage(formatMarkdown(reply), 3500)
		if messageTS != "" {
			err = updateMessage(cfg, j.channel, messageTS, parts[0])
		} else {
			_, err = postMessage(cfg, j, parts[0])
		}
		if err != nil {
			log.Printf("reply: %v", err)
			continue
		}
		for _, part := range parts[1:] {
			if _, err := postMessage(cfg, j, part); err != nil {
				log.Printf("reply: %v", err)
				break
			}
		}
	}
}

func executeJob(cfg config, j job) (string, error) {
	return runAX(cfg, j)
}

func runAX(cfg config, j job) (string, error) {
	session := filepath.Join(cfg.sessions, j.teamID, j.channel, j.threadTS+".jsonl")
	args := []string{"--events", "--session", session, "-C", cfg.workdir}
	if cfg.baseURL != "" {
		args = append(args, "-base", cfg.baseURL)
	}
	if cfg.model != "" {
		args = append(args, "-model", cfg.model)
	}
	if cfg.system != "" {
		args = append(args, "-system", cfg.system)
	}
	args = append(args, j.prompt)
	cmd := exec.Command(cfg.axPath, args...)
	env := []string{
		"AX_SLACK_CHANNEL=" + j.channel,
		"AX_SLACK_THREAD=" + j.threadTS,
		"AX_ARTIFACT_DIR=" + filepath.Join(cfg.sessions, j.teamID, j.channel, "artifacts"),
	}
	if os.Getenv("AX_WORKSPACE") == "" {
		env = append(env, "AX_WORKSPACE="+cfg.workdir)
	}
	cmd.Env = append(os.Environ(), env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	decoder := json.NewDecoder(stdout)
	var result []axMessage
	var eventError string
	var outcome string
	for {
		var event axEvent
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return "", fmt.Errorf("decode AX event: %w", err)
		}
		switch event.Type {
		case "result":
			result = event.Messages
		case "error":
			if err := json.Unmarshal(event.Message, &eventError); err != nil {
				eventError = string(event.Message)
			}
		case "done":
			outcome = event.Outcome
		}
	}
	waitErr := cmd.Wait()
	if eventError != "" {
		return "", errors.New(eventError)
	}
	if waitErr != nil || outcome != "done" {
		message := strings.TrimSpace(stderr.String())
		if message == "" && waitErr != nil {
			message = waitErr.Error()
		}
		if message == "" {
			message = "AX ended with " + outcome
		}
		return "", errors.New(message)
	}
	for i := len(result) - 1; i >= 0; i-- {
		message := result[i]
		if message.Role != "assistant" || strings.TrimSpace(message.Content) == "" {
			continue
		}
		if len(message.ToolCalls) > 0 && string(message.ToolCalls) != "null" && string(message.ToolCalls) != "[]" {
			continue
		}
		return strings.TrimSpace(message.Content), nil
	}
	return "", errors.New("ax returned an empty response")
}

func formatMarkdown(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var output []string
	inCode := false
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			output = append(output, line)
			i++
			continue
		}
		if !inCode && i+1 < len(lines) && isTableDelimiter(lines[i+1]) {
			table := [][]string{splitTable(line)}
			i += 2
			for i < len(lines) {
				row := splitTable(lines[i])
				if len(row) != len(table[0]) {
					break
				}
				table = append(table, row)
				i++
			}
			output = append(output, formatTable(table))
			continue
		}
		if !inCode {
			line = formatLine(line)
		}
		output = append(output, line)
		i++
	}
	return strings.Join(output, "\n")
}

func formatLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if strings.HasPrefix(trimmed, "#") {
		title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if title != "" {
			line = "*" + title + "*"
		}
	}
	line = markdownLink.ReplaceAllString(line, `<$2|$1>`)
	line = markdownBold.ReplaceAllString(line, `*$1*`)
	line = markdownUnderlineBold.ReplaceAllString(line, `*$1*`)
	line = markdownStrike.ReplaceAllString(line, `~$1~`)
	return line
}

func isTableDelimiter(line string) bool {
	cells := splitTable(line)
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if !tableDelimiter.MatchString(cell) {
			return false
		}
	}
	return true
}

func splitTable(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func formatTable(rows [][]string) string {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	var lines []string
	for rowIndex, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = cell + strings.Repeat(" ", widths[i]-len([]rune(cell)))
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		if rowIndex == 0 {
			separators := make([]string, len(widths))
			for i, width := range widths {
				separators[i] = strings.Repeat("-", width)
			}
			lines = append(lines, "|-"+strings.Join(separators, "-|-")+"-|")
		}
	}
	return "```\n" + strings.Join(lines, "\n") + "\n```"
}

func splitMessage(text string, limit int) []string {
	if len([]rune(text)) <= limit {
		return []string{text}
	}
	blocks := strings.Split(text, "\n\n")
	var parts []string
	current := ""
	for _, block := range blocks {
		separator := ""
		if current != "" {
			separator = "\n\n"
		}
		if len([]rune(current+separator+block)) <= limit {
			current += separator + block
			continue
		}
		if current != "" {
			parts = append(parts, current)
			current = ""
		}
		for len([]rune(block)) > limit {
			runes := []rune(block)
			cut := limit
			for cut > limit/2 && runes[cut] != '\n' {
				cut--
			}
			if cut == limit/2 {
				cut = limit
			}
			parts = append(parts, strings.TrimSpace(string(runes[:cut])))
			block = strings.TrimSpace(string(runes[cut:]))
		}
		current = block
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func postMessage(cfg config, j job, text string) (string, error) {
	form := url.Values{
		"channel":   {j.channel},
		"thread_ts": {j.threadTS},
		"text":      {text},
	}
	result, err := slackAPI(cfg, "chat.postMessage", form)
	return result.TS, err
}

func updateMessage(cfg config, channel, ts, text string) error {
	form := url.Values{
		"channel": {channel},
		"ts":      {ts},
		"text":    {text},
	}
	_, err := slackAPI(cfg, "chat.update", form)
	return err
}

func slackAPI(cfg config, method string, form url.Values) (slackResponse, error) {
	var result slackResponse
	req, err := http.NewRequest(http.MethodPost, "https://slack.com/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.botToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return result, err
	}
	if !result.OK {
		return result, errors.New(result.Error)
	}
	return result, nil
}
