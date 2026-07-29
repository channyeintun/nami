package agent

import (
	"regexp"
	"strconv"
	"strings"
)

const progressDirectivePrefix = "::progress{"

// maxProgressDirectiveHold bounds how much of a candidate line the filter will
// hold back before giving up and passing it through as normal text.
const maxProgressDirectiveHold = 256

// maxInTurnPercent caps in-turn progress so the bar never reads complete before
// the turn actually finishes.
const maxInTurnPercent = 99

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
	// held is the candidate directive line accumulated so far.
	held string
	// passingThrough means the current line was already ruled out, so the rest
	// of it streams straight through until the next newline.
	passingThrough bool
}

func newProgressDirectiveFilter() *progressDirectiveFilter {
	return &progressDirectiveFilter{}
}

// Process consumes one streamed chunk and returns the text safe to surface now
// plus any directives completed within it.
func (f *progressDirectiveFilter) Process(chunk string) (string, []GoalProgressUpdate) {
	// Fast path: the common mid-line chunk streams through untouched, with no
	// buffer and no copy.
	if f.passingThrough && strings.IndexByte(chunk, '\n') < 0 {
		return chunk, nil
	}

	var out strings.Builder
	var updates []GoalProgressUpdate

	for chunk != "" {
		lineEnd := strings.IndexByte(chunk, '\n')

		if f.passingThrough {
			if lineEnd < 0 {
				out.WriteString(chunk)
				break
			}
			out.WriteString(chunk[:lineEnd+1])
			chunk = chunk[lineEnd+1:]
			f.passingThrough = false
			continue
		}

		// Incomplete line: hold it until it completes or stops looking like a
		// directive.
		if lineEnd < 0 {
			f.held += chunk
			chunk = ""
			if !couldBeProgressDirective(f.held) {
				out.WriteString(f.held)
				f.held = ""
				f.passingThrough = true
			}
			continue
		}

		line := f.held + chunk[:lineEnd]
		chunk = chunk[lineEnd+1:]
		f.held = ""
		if update, ok := parseProgressDirective(line); ok {
			updates = append(updates, update)
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}

	return out.String(), updates
}

// Flush drains any held text at end of stream, parsing a trailing directive
// that arrived without a final newline.
func (f *progressDirectiveFilter) Flush() (string, []GoalProgressUpdate) {
	held := f.held
	f.held = ""
	f.passingThrough = false
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
	// Either the line already opens with the directive, or it is still a
	// partial prefix of one.
	return strings.HasPrefix(trimmed, progressDirectivePrefix) ||
		strings.HasPrefix(progressDirectivePrefix, trimmed)
}

var progressAttrPattern = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\d+))`)

func parseProgressDirective(line string) (GoalProgressUpdate, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, progressDirectivePrefix) || !strings.HasSuffix(trimmed, "}") {
		return GoalProgressUpdate{}, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(trimmed, progressDirectivePrefix), "}")

	var update GoalProgressUpdate
	found := false
	for _, match := range progressAttrPattern.FindAllStringSubmatch(body, -1) {
		name, quoted, number := match[1], match[2], match[3]
		value := quoted
		if number != "" {
			value = number
		}
		switch name {
		case "goal":
			update.Goal = strings.TrimSpace(value)
		case "label":
			update.Label = strings.TrimSpace(value)
		case "percent":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				continue
			}
			update.Percent = n
		default:
			continue
		}
		found = true
	}
	if !found {
		return GoalProgressUpdate{}, false
	}
	return update, true
}

// GoalProgressState is the monotonic per-turn state behind the goal progress
// indicator, merged from the ::progress directives seen so far this turn.
type GoalProgressState struct {
	Goal    string
	Percent int
	Label   string
}

// Apply merges a parsed directive into the per-turn state, clamping so the bar
// never moves backward and never reads complete before the turn actually
// finishes. It reports whether anything changed; mapping the state onto the
// wire is the streaming layer's job.
func (s *GoalProgressState) Apply(update GoalProgressUpdate) bool {
	changed := false
	if goal := strings.TrimSpace(update.Goal); goal != "" && goal != s.Goal {
		s.Goal = goal
		changed = true
	}
	// An absent percent is the zero value, which can never exceed the monotonic
	// state, so it needs no separate sentinel.
	if percent := min(update.Percent, maxInTurnPercent); percent > s.Percent {
		s.Percent = percent
		changed = true
	}
	if label := strings.TrimSpace(update.Label); label != "" && label != s.Label {
		s.Label = label
		changed = true
	}
	return changed
}
