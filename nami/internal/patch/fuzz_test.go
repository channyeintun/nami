package patch

import (
	"errors"
	"strings"
	"testing"
)

// FuzzParse checks that arbitrary input either parses into a well-formed
// document or fails with a classified *Failure — never a panic and never a bare
// error the tool layer cannot translate.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"*** Begin Patch\n*** End Patch",
		"*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch",
		"*** Begin Patch\n*** Delete File: a.txt\n*** End Patch",
		"*** Begin Patch\n*** Update File: a.txt\n@@\n ctx\n-old\n+new\n*** End Patch",
		"*** Begin Patch\r\n*** Add File: a.txt\r\n+x\r\n*** End Patch\r\n",
		"*** Begin Patch\n*** Update File: a.txt\n@@\n@@\n-x\n+y\n*** End Patch",
		"*** Begin Patch\n*** Add File: \n+x\n*** End Patch",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		document, err := Parse(text)
		if err != nil {
			if _, ok := errors.AsType[*Failure](err); !ok {
				t.Fatalf("Parse(%q) returned %T, want *Failure", text, err)
			}
			return
		}
		for _, operation := range document.Operations {
			if strings.TrimSpace(operation.Path) == "" {
				t.Fatalf("Parse(%q) produced an operation with an empty path", text)
			}
			switch operation.Action {
			case ActionAdd:
				if len(operation.Hunks) != 0 {
					t.Fatalf("add operation carries hunks: %+v", operation)
				}
			case ActionDelete:
				if len(operation.Hunks) != 0 || len(operation.Lines) != 0 {
					t.Fatalf("delete operation carries a body: %+v", operation)
				}
			case ActionUpdate:
				if len(operation.Hunks) == 0 {
					t.Fatalf("update operation has no hunks: %+v", operation)
				}
				for _, hunk := range operation.Hunks {
					assertHunkChanges(t, hunk)
				}
			default:
				t.Fatalf("unknown action %q", operation.Action)
			}
		}
	})
}

func assertHunkChanges(t *testing.T, hunk Hunk) {
	t.Helper()
	for _, line := range hunk.Lines {
		switch line.Kind {
		case ' ':
		case '+', '-':
			return
		default:
			t.Fatalf("hunk line has unknown kind %q", string(line.Kind))
		}
	}
	t.Fatalf("hunk %+v has no +/- line but was kept", hunk)
}

// FuzzApply checks that applying parsed hunks to arbitrary content either fails
// with a classified *Failure or produces content that actually changed and stays
// consistent with the located replacement ranges.
func FuzzApply(f *testing.F) {
	f.Add("alpha\nbeta\ngamma\n", "-beta\n+beta updated")
	f.Add("a\nb\nc\n", " a\n-b\n+B\n c")
	f.Add("dup\ndup\n", "-dup\n+once")
	f.Add("", "+added")
	f.Add("x", "-x\n+y")
	f.Add("prefix-target-suffix\n", "-target\n+replaced")

	f.Fuzz(func(t *testing.T, content, hunkBody string) {
		hunk := Hunk{}
		for _, line := range strings.Split(hunkBody, "\n") {
			kind, value := classifyHunkLine(line)
			hunk.Lines = append(hunk.Lines, Line{Kind: kind, Value: value})
		}

		updated, err := Apply(content, "fuzz.txt", []Hunk{hunk})
		if err != nil {
			if _, ok := errors.AsType[*Failure](err); !ok {
				t.Fatalf("Apply returned %T, want *Failure", err)
			}
			return
		}
		if updated == content {
			t.Fatalf("Apply reported success without changing content %q", content)
		}

		replacement, err := LocateHunk(content, "fuzz.txt", hunk)
		if err != nil {
			t.Fatalf("Apply succeeded but LocateHunk failed: %v", err)
		}
		if replacement.Start < 0 || replacement.End > len(content) || replacement.Start > replacement.End {
			t.Fatalf("replacement range %d:%d out of bounds for %d bytes", replacement.Start, replacement.End, len(content))
		}
		if content[replacement.Start:replacement.End] != replacement.OldBlock {
			t.Fatalf("replacement range does not cover the old block")
		}
		if want := content[:replacement.Start] + replacement.NewBlock + content[replacement.End:]; updated != want {
			t.Fatalf("Apply = %q, want %q", updated, want)
		}
	})
}
