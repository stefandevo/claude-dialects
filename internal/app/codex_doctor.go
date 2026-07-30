package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	codexDiagnosticMaxAge = 30 * time.Minute
	codexFastResponse     = 10 * time.Millisecond
	codexLogReadLimit     = 128 * 1024
)

var (
	codexUpstreamStatusPattern = regexp.MustCompile(`(?m)^HTTP Status:\s*(502|503)\s*$`)
	codexTimestampPattern      = regexp.MustCompile(`(?m)^Timestamp:\s*(\S+)\s*$`)
	codexProxyStatusPattern    = regexp.MustCompile(`(?:^|\|)\s*(502|503)\s*\|\s*([0-9.]+(?:ns|µs|us|ms|s|m|h))\s*\|`)
	codexProxyTimePattern      = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]`)
)

type codexUpstreamFailure struct {
	status      int
	model       string
	occurredAt  time.Time
	fastRetries bool
}

func codexUpstreamDiagnostics(name string, dialect Dialect) []string {
	if !slices.Contains(expectedAuthProviders(dialect), "codex") || !hasProviderCredentials(name, "codex") {
		return nil
	}
	now := time.Now()
	failure, ok := recentCodexUpstreamFailure(name, now)
	if !ok {
		return nil
	}
	failure.fastRetries = hasRecentFastCodexRetry(name, failure.occurredAt, now)

	upstream := "Codex"
	if failure.model != "" {
		upstream = failure.model
	}
	lines := []string{fmt.Sprintf(
		"⚠ %s: %s upstream returned %d over HTTP. OAuth credentials are present.",
		name, upstream, failure.status,
	)}
	if failure.fastRetries {
		lines = append(lines,
			"  Retries completing in under 10ms indicate a CLIProxyAPI credential cooldown; auth_unavailable does not mean OAuth is missing.",
		)
	} else {
		lines = append(lines,
			"  This failure can put the credential in cooldown (usually about 1 minute); retries may report auth_unavailable without another upstream request.",
		)
	}

	modelAlternative := "choose another model with /model"
	if tier, model := codexAlternativeModel(dialect, failure.model); tier != "" {
		modelAlternative = "/model " + tier + " (" + model + ")"
	}
	lines = append(lines,
		fmt.Sprintf("  Try: %s or cc-dialect proxy %s restart", modelAlternative, name),
		fmt.Sprintf("  Details: cc-dialect proxy %s logs", name),
		"  Note: Codex CLI uses WebSocket upstream; this dialect uses HTTP, so model availability can differ.",
	)
	return lines
}

func recentCodexUpstreamFailure(name string, now time.Time) (codexUpstreamFailure, bool) {
	instance, err := openInstanceFS(name)
	if err != nil {
		return codexUpstreamFailure{}, false
	}
	defer instance.Close()

	entries, err := instance.ReadDir("auth/logs")
	if err != nil {
		return codexUpstreamFailure{}, false
	}

	var latest codexUpstreamFailure
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "error-v1-messages-") || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		file, openErr := instance.Open("auth/logs/" + entry.Name())
		if openErr != nil {
			continue
		}
		content, info, readErr := readLogEdges(file, codexLogReadLimit)
		_ = file.Close()
		if readErr != nil || !strings.Contains(strings.ToLower(string(content)), "upstream transport: http") {
			continue
		}
		statusMatch := codexUpstreamStatusPattern.FindSubmatch(content)
		if len(statusMatch) != 2 {
			continue
		}
		status, parseErr := strconv.Atoi(string(statusMatch[1]))
		if parseErr != nil {
			continue
		}
		occurredAt := info.ModTime()
		if timestampMatch := codexTimestampPattern.FindSubmatch(content); len(timestampMatch) == 2 {
			if parsed, timestampErr := time.Parse(time.RFC3339Nano, string(timestampMatch[1])); timestampErr == nil {
				occurredAt = parsed
			}
		}
		if occurredAt.After(now.Add(time.Minute)) || now.Sub(occurredAt) > codexDiagnosticMaxAge {
			continue
		}
		if latest.occurredAt.IsZero() || occurredAt.After(latest.occurredAt) {
			latest = codexUpstreamFailure{
				status:     status,
				model:      codexRequestModel(content),
				occurredAt: occurredAt,
			}
		}
	}
	return latest, !latest.occurredAt.IsZero()
}

func codexRequestModel(content []byte) string {
	for _, marker := range [][]byte{
		[]byte("=== REQUEST BODY ===\n"),
		[]byte("=== API REQUEST ===\n"),
	} {
		start := bytes.Index(content, marker)
		if start < 0 {
			continue
		}
		section := content[start+len(marker):]
		if end := bytes.Index(section, []byte("\n\n=== ")); end >= 0 {
			section = section[:end]
		}
		if model := topLevelJSONModel(section); model != "" {
			return model
		}
	}
	return ""
}

func topLevelJSONModel(content []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	for decoder.More() {
		key, keyErr := decoder.Token()
		if keyErr != nil {
			return ""
		}
		if key == "model" {
			var model string
			if decoder.Decode(&model) == nil {
				return strings.TrimSpace(model)
			}
			return ""
		}
		var ignored json.RawMessage
		if decoder.Decode(&ignored) != nil {
			return ""
		}
	}
	return ""
}

func codexAlternativeModel(dialect Dialect, failedModel string) (tier, model string) {
	if failedModel == "" {
		return "", ""
	}
	for _, candidate := range []struct {
		tier  string
		model string
	}{
		{tier: "sonnet", model: dialect.SonnetModel},
		{tier: "haiku", model: dialect.HaikuModel},
		{tier: "opus", model: dialect.OpusModel},
	} {
		if candidate.model != "" && candidate.model != failedModel {
			return candidate.tier, candidate.model
		}
	}
	return "", ""
}

func hasRecentFastCodexRetry(name string, since, now time.Time) bool {
	instance, err := openInstanceFS(name)
	if err != nil {
		return false
	}
	defer instance.Close()

	file, err := instance.Open("proxy.log")
	if err != nil {
		return false
	}
	content, info, readErr := readLogTail(file, codexLogReadLimit)
	_ = file.Close()
	if readErr != nil || (now.Sub(info.ModTime()) > codexDiagnosticMaxAge && !info.ModTime().After(now)) {
		return false
	}

	for _, line := range strings.Split(string(content), "\n") {
		match := codexProxyStatusPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		if timestampMatch := codexProxyTimePattern.FindStringSubmatch(line); len(timestampMatch) == 2 {
			occurredAt, timestampErr := time.ParseInLocation("2006-01-02 15:04:05", timestampMatch[1], now.Location())
			if timestampErr == nil {
				// Access-log timestamps have second precision, while request-log
				// timestamps retain nanoseconds. Allow the same wall-clock second
				// rather than rejecting a retry that followed moments later.
				if occurredAt.Before(since.Add(-time.Second)) ||
					occurredAt.After(now.Add(time.Minute)) ||
					now.Sub(occurredAt) > codexDiagnosticMaxAge {
					continue
				}
			}
		}
		durationText := strings.ReplaceAll(match[2], "us", "µs")
		duration, durationErr := time.ParseDuration(durationText)
		if durationErr == nil && duration < codexFastResponse {
			return true
		}
	}
	return false
}

func readLogEdges(file *os.File, limit int64) ([]byte, os.FileInfo, error) {
	head, info, err := readLogSection(file, 0, limit)
	if err != nil {
		return nil, info, err
	}
	if info.Size() <= limit {
		return head, info, nil
	}
	tail, _, err := readLogSection(file, max(info.Size()-limit, 0), limit)
	if err != nil {
		return nil, info, err
	}
	return append(append(head, '\n'), tail...), info, nil
}

func readLogTail(file *os.File, limit int64) ([]byte, os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	return readLogSection(file, max(info.Size()-limit, 0), limit)
}

func readLogSection(file *os.File, offset, limit int64) ([]byte, os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	reader := io.NewSectionReader(file, offset, min(limit, max(info.Size()-offset, 0)))
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, info, err
	}
	return content, info, nil
}
