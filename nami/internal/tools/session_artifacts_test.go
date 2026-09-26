package tools

import "testing"

// A fetch report is upserted by slot, so two URLs that map to the same slot
// overwrite each other's report. The readable part of the slot keeps only the
// first 40 bytes of the URL, which every page of one documentation site or one
// repository shares.
func TestWebFetchArtifactSlotIsUniquePerURL(t *testing.T) {
	pages := []string{
		"https://developer.mozilla.org/en-US/docs/Web/API/Fetch_API",
		"https://developer.mozilla.org/en-US/docs/Web/API/Streams_API",
		"https://github.com/channyeintun/nami/blob/main/README.md",
		"https://github.com/channyeintun/nami/blob/main/docs/orchestration-and-goal-loops.md",
	}
	seen := make(map[string]string)
	for _, page := range pages {
		slot := webFetchArtifactSlot(page)
		if other, ok := seen[slot]; ok {
			t.Fatalf("%q and %q share the report slot %q", other, page, slot)
		}
		seen[slot] = page
	}
	first, refetched := webFetchArtifactSlot(pages[0]), webFetchArtifactSlot(pages[0])
	if first != refetched {
		t.Fatalf("slots %q and %q differ; refetching a URL must update its report", first, refetched)
	}
}
