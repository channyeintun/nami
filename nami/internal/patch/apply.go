package patch

import (
	"fmt"
	"sort"
	"strings"
)

// Replacement is a resolved hunk: the byte range in the source content that the
// hunk replaces, plus the text that takes its place.
type Replacement struct {
	Start    int
	End      int
	OldBlock string
	NewBlock string
}

// Apply resolves every hunk against content and returns the patched result.
// content must already use "\n" line endings. Errors are always a *Failure.
func Apply(content, path string, hunks []Hunk) (string, error) {
	if len(hunks) == 0 {
		return "", newFailure(FailureInvalidFormat, path, fmt.Sprintf("update section for %s did not contain any hunks", path), "Add one or more hunks with context lines and +/- changes under *** Update File.")
	}

	replacements := make([]Replacement, 0, len(hunks))
	for _, hunk := range hunks {
		replacement, err := LocateHunk(content, path, hunk)
		if err != nil {
			return "", err
		}
		replacements = append(replacements, replacement)
	}

	// Apply from the end of the file backwards so earlier offsets stay valid.
	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].Start > replacements[j].Start
	})
	for i := 1; i < len(replacements); i++ {
		if replacements[i-1].Start < replacements[i].End {
			return "", newFailure(FailureOverlap, path, fmt.Sprintf("patch hunks overlap in %s", path), "Split the patch into non-overlapping hunks or merge the overlapping edits into a single hunk.")
		}
	}

	updated := content
	for _, replacement := range replacements {
		updated = updated[:replacement.Start] + replacement.NewBlock + updated[replacement.End:]
	}
	if updated == content {
		return "", newFailure(FailureNoOp, path, fmt.Sprintf("patch did not change %s", path), "Adjust the hunk contents or skip this file if it is already in the desired state.")
	}
	return updated, nil
}

// LocateHunk finds the unique place in content that a hunk applies to. The old
// block has to match whole lines: a hunk for "foo" must not match the middle of
// "prefix-foo-suffix".
func LocateHunk(content, path string, hunk Hunk) (Replacement, error) {
	oldLines := make([]string, 0, len(hunk.Lines))
	newLines := make([]string, 0, len(hunk.Lines))
	hasChange := false
	for _, line := range hunk.Lines {
		switch line.Kind {
		case ' ':
			oldLines = append(oldLines, line.Value)
			newLines = append(newLines, line.Value)
		case '-':
			hasChange = true
			oldLines = append(oldLines, line.Value)
		case '+':
			hasChange = true
			newLines = append(newLines, line.Value)
		default:
			return Replacement{}, newFailure(FailureInvalidFormat, path, fmt.Sprintf("unsupported hunk line kind %q in %s", string(line.Kind), path), "Use context lines, - removals, and + additions inside update hunks.")
		}
	}
	if !hasChange {
		return Replacement{}, newFailure(FailureInvalidFormat, path, fmt.Sprintf("update hunk for %s does not contain any changes", path), "Include at least one - removal or + addition in each update hunk.")
	}

	oldBlock := strings.Join(oldLines, "\n")
	newBlock := strings.Join(newLines, "\n")
	start, matchCount := findLineAlignedMatch(content, oldBlock)
	if matchCount == 0 {
		return Replacement{}, newFailure(FailureNoMatch, path, fmt.Sprintf("patch hunk did not match the current file contents: %s", path), "Reread the file and refresh the patch context so the old block matches exactly.")
	}
	if matchCount > 1 {
		return Replacement{}, newFailure(FailureMultipleMatch, path, fmt.Sprintf("patch hunk matched multiple locations in %s", path), "Include more surrounding context lines in the hunk so it matches exactly once.")
	}
	return Replacement{
		Start:    start,
		End:      start + len(oldBlock),
		OldBlock: oldBlock,
		NewBlock: newBlock,
	}, nil
}

// findLineAlignedMatch returns the offset of the first occurrence of needle
// that starts and ends on a line boundary, along with how many such occurrences
// exist. A needle that only appears inside a longer line does not count: a
// patch describes whole lines, and replacing a fragment would corrupt the file.
func findLineAlignedMatch(content, needle string) (int, int) {
	if needle == "" {
		return -1, 0
	}
	count := 0
	firstIndex := -1
	for offset := 0; offset <= len(content)-len(needle); {
		index := strings.Index(content[offset:], needle)
		if index < 0 {
			break
		}
		absoluteIndex := offset + index
		if startsLine(content, absoluteIndex) && endsLine(content, absoluteIndex+len(needle)) {
			if count == 0 {
				firstIndex = absoluteIndex
			}
			count++
		}
		offset = absoluteIndex + 1
	}
	return firstIndex, count
}

func startsLine(content string, index int) bool {
	return index == 0 || content[index-1] == '\n'
}

func endsLine(content string, index int) bool {
	return index == len(content) || content[index] == '\n'
}
