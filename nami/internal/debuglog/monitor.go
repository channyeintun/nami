package debuglog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// maxSummaryChars caps the fallback one-line summary of a log entry.
const maxSummaryChars = 140

type MonitorOptions struct {
	FilePath  string
	Level     string
	Component string
	Event     string
	Raw       bool
	Lines     int
}

func RunMonitor(options MonitorOptions) error {
	path := strings.TrimSpace(options.FilePath)
	if path == "" {
		return fmt.Errorf("--file is required")
	}

	if !options.Raw {
		fmt.Printf("Debug monitor: %s\n", path)
		fmt.Printf("Filters: level=%s component=%s event=%s\n\n", filterValue(options.Level), filterValue(options.Component), filterValue(options.Event))
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if options.Lines > 0 {
		if err := printRecentLines(file, options); err != nil {
			return err
		}
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	reader := bufio.NewReader(file)

	for {
		line, err := reader.ReadString('\n')
		if err == nil {
			printMonitorLine(line, options)
			continue
		}
		if err != io.EOF {
			return err
		}

		info, statErr := os.Stat(path)
		if statErr == nil {
			position, seekErr := file.Seek(0, io.SeekCurrent)
			if seekErr == nil && info.Size() < position {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					return err
				}
				reader.Reset(file)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func OpenMonitorPopup(filePath string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic debug monitor popup is currently supported on macOS only")
	}

	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(execPath); resolveErr == nil {
		execPath = resolved
	}
	commandLine := debugViewCommandLine(execPath, filePath)
	script := []string{
		`tell application "Terminal"`,
		`activate`,
		fmt.Sprintf(`do script %q`, commandLine),
		`end tell`,
	}
	return exec.Command("osascript", flattenAppleScript(script)...).Start()
}

func printRecentLines(file *os.File, options MonitorOptions) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	scanner := bufio.NewScanner(file)
	lines := make([]string, 0, options.Lines)
	for scanner.Scan() {
		line := scanner.Text()
		if len(lines) == options.Lines {
			copy(lines, lines[1:])
			lines[len(lines)-1] = line
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	for _, line := range lines {
		printMonitorLine(line, options)
	}
	return nil
}

func printMonitorLine(line string, options MonitorOptions) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	var envelope Envelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		fmt.Println(trimmed)
		return
	}
	if !matchesFilters(envelope, options) {
		return
	}

	if options.Raw {
		fmt.Println(trimmed)
		return
	}

	level := strings.ToUpper(filterValue(envelope.Level))
	stamp := envelope.TS
	if len(stamp) >= 19 {
		stamp = stamp[11:19]
	}
	summary := summarizeEnvelope(envelope)
	lineText := fmt.Sprintf("[%s] %-5s %-12s %-20s %s", stamp, level, filterValue(envelope.Component), envelope.Event, summary)
	if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
		lineText += " | error=" + envelope.Error.Message
	}
	fmt.Println(lineText)
}

func matchesFilters(envelope Envelope, options MonitorOptions) bool {
	if options.Level != "" && !strings.EqualFold(envelope.Level, options.Level) {
		return false
	}
	if options.Component != "" && !strings.EqualFold(envelope.Component, options.Component) {
		return false
	}
	if options.Event != "" && !strings.EqualFold(envelope.Event, options.Event) {
		return false
	}
	return true
}

func summarizeEnvelope(envelope Envelope) string {
	parts := make([]string, 0, 6)
	appendField := func(key string) {
		if envelope.Data == nil {
			return
		}
		value, ok := envelope.Data[key]
		if !ok {
			return
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return
		}
		parts = append(parts, key+"="+text)
	}

	appendField("type")
	appendField("tool_name")
	appendField("tool_id")
	appendField("model")
	appendField("stop_reason")
	appendField("message_count")

	if envelope.Metrics != nil {
		if bytesValue, ok := envelope.Metrics["bytes"]; ok {
			parts = append(parts, "bytes="+fmt.Sprint(bytesValue))
		}
		if durationValue, ok := envelope.Metrics["duration_ms"]; ok {
			parts = append(parts, "duration_ms="+fmt.Sprint(durationValue))
		}
	}

	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	if envelope.Data != nil {
		payload, err := json.Marshal(envelope.Data)
		if err == nil {
			return Truncate(string(payload), maxSummaryChars)
		}
	}
	return "-"
}

func debugViewCommandLine(execPath string, filePath string) string {
	return shellQuote(execPath) + " debug-view --file " + shellQuote(filePath)
}

func flattenAppleScript(lines []string) []string {
	args := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		args = append(args, "-e", line)
	}
	return args
}

// shellQuote wraps a value in single quotes for a POSIX shell. An embedded
// quote has to close the literal, contribute a double-quoted quote, and reopen
// it — '"'"' — otherwise the command ends up with an unterminated string.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func filterValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "*"
	}
	return trimmed
}
