package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/channyeintun/nami/internal/ipc"
)

const progressDirectivePrefix = "::progress{"

// maxProgressDirectiveHold bounds how much of a candidate line the filter will
// hold back before giving up and passing it through as normal text.
const maxProgressDirectiveHold = 256

// GoalProgressUpdate is one parsed ::progress directive from the assistant stream.
type GoalProgressUpdate struct {
	Goal    string
	Percent int
	Label   string
}

// progressDirectiveFilter strips ::progress{...} directive lines out of
// streamed assistant text. It holds back only line prefixes that could still
// become a directive, so ordinary text keeps streaming without buffering.
type progressDirectiveFilter struct {
	held        string
	atLineStart bool
}

func newProgressDirectiveFilter() *progressDirectiveFilter {
	return &progressDirectiveFilter{atLineStart: true}
}

// Process consumes one streamed chunk and returns the text safe to surface now
// plus any directives completed within it.
func (f *progressDirectiveFilter) Process(chunk string) (string, []GoalProgressUpdate) {
	var out strings.Builder
	var updates []GoalProgressUpdate

	for chunk != "" {
		if !f.atLineStart && f.held == "" {
			lineEnd := strings.IndexByte(chunk, '\n')
			if lineEnd < 0 {
				out.WriteString(chunk)
				return out.String(), updates
			}
			out.WriteString(chunk[:lineEnd+1])
			chunk = chunk[lineEnd+1:]
			f.atLineStart = true
			continue
		}

		lineEnd := strings.IndexByte(chunk, '\n')
		if lineEnd < 0 {
			f.held += chunk
			chunk = ""
			if !couldBeProgressDirective(f.held) {
				out.WriteString(f.held)
				f.held = ""
				f.atLineStart = false
			}
			continue
		}

		f.held += chunk[:lineEnd]
		chunk = chunk[lineEnd+1:]
		if update, ok := parseProgressDirective(f.held); ok {
			updates = append(updates, update)
		} else {
			out.WriteString(f.held)
			out.WriteByte('\n')
		}
		f.held = ""
		f.atLineStart = true
	}

	return out.String(), updates
}

// Flush drains any held text at end of stream, parsing a trailing directive
// that arrived without a final newline.
func (f *progressDirectiveFilter) Flush() (string, []GoalProgressUpdate) {
	held := f.held
	f.held = ""
	f.atLineStart = true
	if held == "" {
		return "", nil
	}
	if update, ok := parseProgressDirective(held); ok {
		return "", []GoalProgressUpdate{update}
	}
	return held, nil
}

func couldBeProgressDirective(held string) bool {
	if len(held) > maxProgressDirectiveHold {
		return false
	}
	trimmed := strings.TrimLeft(held, " \t")
	if len(trimmed) < len(progressDirectivePrefix) {
		return strings.HasPrefix(progressDirectivePrefix, trimmed)
	}
	return strings.HasPrefix(trimmed, progressDirectivePrefix)
}

var progressAttrPattern = regexp.MustCompile(`(\w+)=("([^"]*)"|[0-9]+)`)

func parseProgressDirective(line string) (GoalProgressUpdate, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, progressDirectivePrefix) || !strings.HasSuffix(trimmed, "}") {
		return GoalProgressUpdate{}, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(trimmed, progressDirectivePrefix), "}")
	update := GoalProgressUpdate{Percent: -1}
	found := false
	for _, match := range progressAttrPattern.FindAllStringSubmatch(body, -1) {
		value := match[2]
		if strings.HasPrefix(value, `"`) {
			value = match[3]
		}
		switch match[1] {
		case "goal":
			update.Goal = strings.TrimSpace(value)
			found = true
		case "label":
			update.Label = strings.TrimSpace(value)
			found = true
		case "percent":
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				update.Percent = n
				found = true
			}
		}
	}
	if !found {
		return GoalProgressUpdate{}, false
	}
	return update, true
}

// applyGoalProgress merges a parsed directive into per-turn progress state,
// clamping so the bar never moves backward and never reads complete before the
// turn actually finishes. It returns the payload to emit and whether anything
// changed.
func (s *QueryState) applyGoalProgress(update GoalProgressUpdate) (ipc.GoalProgressPayload, bool) {
	changed := false
	if goal := strings.TrimSpace(update.Goal); goal != "" && goal != s.ProgressGoal {
		s.ProgressGoal = goal
		changed = true
	}
	if update.Percent >= 0 {
		percent := update.Percent
		if percent > 99 {
			percent = 99
		}
		if percent > s.ProgressPercent {
			s.ProgressPercent = percent
			changed = true
		}
	}
	if label := strings.TrimSpace(update.Label); label != "" && label != s.ProgressLabel {
		s.ProgressLabel = label
		changed = true
	}
	if !changed {
		return ipc.GoalProgressPayload{}, false
	}
	return ipc.GoalProgressPayload{
		Goal:    s.ProgressGoal,
		Percent: s.ProgressPercent,
		Label:   s.ProgressLabel,
	}, true
}
