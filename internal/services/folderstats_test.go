package services

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/tonyflux/pkp/internal/index"
)

func TestFolderStatsFromTheIndex(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	h1, h3 := strings.Repeat("1", 40), strings.Repeat("3", 40)
	copyOf := func(r index.Row, hash string) index.Row {
		r.Hash = hash
		return r
	}
	as := func(r index.Row, category, mime string) index.Row {
		r.Category, r.MimeType, r.FileExtension = category, mime, ""
		return r
	}
	at := "2026-01-01T00:00:00Z"
	trashed := indexedVideo("tr", "A", "tr.mkv", 1000, at)
	trashed.Trashed = true
	// A/ a1 a2 (one content) S/ s1 (image) s2 (document) s3 (A's content
	// again); r1 at the root is a fourth copy outside A; B/ b1 b2 a pair.
	fillIndex(t, svc, []index.Row{
		indexedFolder("A", "", "影视"),
		copyOf(indexedVideo("a1", "A", "a1.mkv", 100, at), h1),
		copyOf(indexedVideo("a2", "A", "a2.mkv", 100, at), h1),
		trashed,
		indexedFolder("S", "A", "S"),
		as(indexedVideo("s1", "S", "s1.jpg", 50, at), "image", "image/jpeg"),
		as(indexedVideo("s2", "S", "s2.pdf", 30, at), "document", "application/pdf"),
		copyOf(indexedVideo("s3", "S", "s3.mkv", 100, at), h1),
		copyOf(indexedVideo("r1", "", "r1.mkv", 100, at), h1),
		indexedFolder("B", "", "B"),
		copyOf(as(indexedVideo("b1", "B", "b1.jpg", 10, at), "image", "image/jpeg"), h3),
		copyOf(as(indexedVideo("b2", "B", "b2.jpg", 10, at), "image", "image/jpeg"), h3),
	})
	plays := activePlays(t, svc)
	for _, r := range []playRecord{
		{FileID: "a1", Position: 500, Duration: 1000, PlayedAt: "2026-10-12T05:00:00Z"},  // unfinished
		{FileID: "s3", Position: 60, Duration: 1000, PlayedAt: "2026-10-12T06:00:00Z"},   // unfinished, deeper
		{FileID: "a2", Position: 1000, Duration: 1000, PlayedAt: "2026-10-12T07:00:00Z"}, // finished
		{FileID: "r1", Position: 500, Duration: 1000, PlayedAt: "2026-10-12T08:00:00Z"},  // outside A
	} {
		plays.opened(r)
	}

	got, err := svc.FolderStats("", "A")
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 5 || got.Folders != 1 || got.Size != 380 {
		t.Errorf("A counts = %d files, %d folders, %d bytes", got.Files, got.Folders, got.Size)
	}
	wantCats := []CategoryStat{{"video", 3, 300}, {"image", 1, 50}, {"document", 1, 30}}
	if !slices.Equal(got.Categories, wantCats) {
		t.Errorf("A categories = %+v", got.Categories)
	}
	if got.DupGroups != 1 || got.DupCopies != 2 || got.DupWaste != 200 {
		t.Errorf("A duplicates = %d groups, %d copies, %d bytes", got.DupGroups, got.DupCopies, got.DupWaste)
	}
	if got.Unfinished != 2 {
		t.Errorf("A unfinished = %d, want 2", got.Unfinished)
	}
	if ids := largestIDs(got.Largest); !slices.Equal(ids, []string{"S", "a1", "a2"}) || got.Largest[0].Size != 180 || !got.Largest[0].Folder {
		t.Errorf("A largest = %v (%+v)", ids, got.Largest)
	}

	got, err = svc.FolderStats("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 8 || got.Folders != 3 || got.Size != 500 {
		t.Errorf("drive counts = %d files, %d folders, %d bytes", got.Files, got.Folders, got.Size)
	}
	if got.DupGroups != 2 || got.DupCopies != 4 || got.DupWaste != 310 {
		t.Errorf("drive duplicates = %d groups, %d copies, %d bytes", got.DupGroups, got.DupCopies, got.DupWaste)
	}
	if got.Unfinished != 3 {
		t.Errorf("drive unfinished = %d, want 3", got.Unfinished)
	}
	if ids := largestIDs(got.Largest); !slices.Equal(ids, []string{"A", "r1", "B"}) || got.Largest[0].Size != 380 {
		t.Errorf("drive largest = %v", ids)
	}

	for _, bad := range []string{"tr", "a1", "missing"} {
		if _, err := svc.FolderStats("", bad); !errors.Is(err, errNoSuchFolder) {
			t.Errorf("FolderStats(%q) = %v, want errNoSuchFolder", bad, err)
		}
	}
}

func largestIDs(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}
