package services

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

func TestBreakdownOfToleratesOnlySmallDrift(t *testing.T) {
	sums := map[string]int64{"video": 600, "image": 200, "document": 100, "audio": 60, "archive": 30, "other": 10}
	const gib = int64(1) << 30
	big := map[string]int64{"video": 3000 * gib}
	for _, tc := range []struct {
		name  string
		sums  map[string]int64
		usage int64
		ok    bool
	}{
		{"exact", sums, 1000, true},
		{"within the 1 MiB floor", sums, 1000 + usageSlack, true},
		{"beyond the 1 MiB floor", sums, 1000 + usageSlack + 1, false},
		{"within a thousandth of a large usage", big, 3000*gib + 3*gib - 1, true},
		{"beyond a thousandth of a large usage", big, 3000*gib + 4*gib, false},
		{"index ahead of the server", big, 2990 * gib, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := breakdownOf(tc.sums, tc.usage, "2026-10-13T00:00:00Z")
			if (b != nil) != tc.ok {
				t.Fatalf("breakdown = %+v, want present=%v", b, tc.ok)
			}
		})
	}

	b := breakdownOf(sums, 1000, "2026-10-13T00:00:00Z")
	want := UsageBreakdown{Video: 600, Image: 200, Document: 100, Other: 100, SyncedAt: "2026-10-13T00:00:00Z"}
	if *b != want {
		t.Errorf("breakdown = %+v, want %+v (audio, archive and other folded into Other)", *b, want)
	}
}

// quotaService answers /drive/v1/about with the given usage.
func quotaService(t *testing.T, usage int64) *DriveService {
	return newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/drive/v1/about" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"kind":"drive#about","quota":{"limit":"%d","usage":"%d","usage_in_trash":"300","is_unlimited":false}}`,
			int64(1)<<40, usage)
	})
}

// seedUsage fills the index with 1000 bytes over four categories, 300 of
// them in the recycle bin, and marks a full walk done when ready.
func seedUsage(t *testing.T, svc *DriveService, ready bool) {
	t.Helper()
	s := activeIndex(svc)
	if s == nil {
		t.Fatal("index did not open")
	}
	rows := []index.Row{
		{ID: "dir", Kind: drive.KindFolder, Name: "dir", Category: "folder", Size: 4096},
		{ID: "v1", ParentID: "dir", Kind: drive.KindFile, Name: "a.mkv", Category: "video", Size: 500},
		{ID: "v2", Kind: drive.KindFile, Name: "b.mkv", Category: "video", Size: 300, Trashed: true},
		{ID: "i1", Kind: drive.KindFile, Name: "c.png", Category: "image", Size: 120},
		{ID: "a1", Kind: drive.KindFile, Name: "d.mp3", Category: "audio", Size: 80},
	}
	if err := s.store.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetMeta(index.MetaSyncedAt, "2026-10-13T01:02:03Z"); err != nil {
		t.Fatal(err)
	}
	if ready {
		if err := s.store.SetMeta(index.MetaFullAt, "2026-10-13T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuotaBreakdown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage int64
		ready bool
		want  *UsageBreakdown
	}{
		{"consistent", 1000 + 11, true, &UsageBreakdown{Video: 800, Image: 120, Other: 80, SyncedAt: "2026-10-13T01:02:03Z"}},
		{"drift beyond tolerance", 1000 + 2*usageSlack, true, nil},
		{"index not ready", 1000, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := quotaService(t, tc.usage)
			seedUsage(t, svc, tc.ready)
			q, err := svc.Quota()
			if err != nil {
				t.Fatalf("quota: %v", err)
			}
			if q.Usage != tc.usage || q.UsageInTrash != 300 {
				t.Errorf("quota = %+v", q)
			}
			switch {
			case tc.want == nil && q.Breakdown != nil:
				t.Errorf("breakdown = %+v, want none", *q.Breakdown)
			case tc.want != nil && (q.Breakdown == nil || *q.Breakdown != *tc.want):
				t.Errorf("breakdown = %+v, want %+v", q.Breakdown, *tc.want)
			}
		})
	}
}

// The inspector shows a folder's child count only when the index has
// walked the whole drive; before that it could only undercount.
func TestFileDetailChildrenNeedAReadyIndex(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprint("ready=", ready), func(t *testing.T) {
			svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
				if authAndTokenRoutes(w, r) {
					return
				}
				switch r.URL.Path {
				case "/drive/v1/files/dir":
					io.WriteString(w, `{"kind":"drive#folder","id":"dir","name":"dir"}`)
				case "/drive/v1/files/v1":
					io.WriteString(w, `{"kind":"drive#file","id":"v1","name":"a.mkv","parent_id":"dir"}`)
				default:
					http.NotFound(w, r)
				}
			})
			seedUsage(t, svc, ready)
			s := activeIndex(svc)
			if err := s.store.Upsert([]index.Row{
				{ID: "sub", ParentID: "dir", Kind: drive.KindFolder, Name: "sub", Category: "folder"},
				{ID: "gone", ParentID: "dir", Kind: drive.KindFile, Name: "gone.txt", Category: "document", Trashed: true},
			}); err != nil {
				t.Fatal(err)
			}

			detail, err := svc.FileDetail("dir")
			if err != nil {
				t.Fatalf("detail: %v", err)
			}
			want := -1
			if ready {
				want = 2 // v1 and sub; the trashed file does not count
			}
			if detail.Children != want {
				t.Errorf("folder children = %d, want %d", detail.Children, want)
			}

			file, err := svc.FileDetail("v1")
			if err != nil {
				t.Fatalf("detail: %v", err)
			}
			if file.Children != -1 {
				t.Errorf("file children = %d, want -1", file.Children)
			}
		})
	}
}
