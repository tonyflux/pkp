package services

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

func aggregateFixture(t *testing.T) (*DriveService, *accountSession, *accountSession) {
	t.Helper()
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if !authAndTokenRoutes(w, r) {
			io.WriteString(w, `{"files":[]}`)
		}
	})
	a := svc.current
	b := seedSession(t, svc, "second@example.com")
	for _, s := range []*accountSession{a, b} {
		if err := s.index().store.SetMeta(index.MetaFullAt, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return svc, a, b
}

func TestAggregatePagesOrderingAndOwners(t *testing.T) {
	svc, a, b := aggregateFixture(t)
	for _, s := range []*accountSession{a, b} {
		rows := []index.Row{{ID: "folder", Kind: drive.KindFolder, Name: s.key}}
		for i := range 240 {
			rows = append(rows, index.Row{ID: fmt.Sprintf("%03d", i), ParentID: "folder", Kind: drive.KindFile, Name: fmt.Sprintf("File %03d", i/2), Category: "video", Size: int64(i % 5), ModifiedTime: fmt.Sprintf("2026-01-%02d", i%28+1)})
		}
		if err := s.index().store.Upsert(rows); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"name", "size", "modified"} {
		for _, asc := range []bool{true, false} {
			req := ListRequest{View: "trash", ParentID: "ignored", Offset: 999, PageToken: "ignored", Sort: key, Asc: asc}
			var got []AggregateEntry
			cursor := ""
			for {
				page, err := svc.ListAllAccounts(req, cursor, nil)
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != 480 {
					t.Fatalf("total = %d", page.Total)
				}
				got = append(got, page.Entries...)
				if page.Next == "" {
					break
				}
				cursor = page.Next
			}
			if len(got) != 480 {
				t.Fatalf("%s: got %d entries", key, len(got))
			}
			seen := map[string]bool{}
			for i, e := range got {
				k := e.Account + "/" + e.Entry.ID
				if seen[k] {
					t.Fatalf("duplicate %s", k)
				}
				seen[k] = true
				if e.Entry.Path != "全部文件 / "+e.Account {
					t.Fatalf("wrong owner path: %+v", e)
				}
				if i > 0 {
					prev := got[i-1]
					row := func(e AggregateEntry) index.Row {
						return index.Row{ID: e.Entry.ID, Name: e.Entry.Name, Size: e.Entry.Size, ModifiedTime: e.Entry.ModifiedTime}
					}
					if aggregateLess(row(e), e.Account, nil, row(prev), prev.Account, nil, key, asc) {
						t.Fatalf("out of order %s: %+v before %+v", key, prev, e)
					}
				}
			}
		}
	}
	if svc.current != b {
		t.Fatal("listing switched current account")
	}
}

func TestAggregateFiltersStatusAndCursor(t *testing.T) {
	svc, a, b := aggregateFixture(t)
	rows := []index.Row{{ID: "dir", Kind: drive.KindFolder, Name: "Media"}, {ID: "1", ParentID: "dir", Kind: drive.KindFile, Name: "A_Clip.mp4", FileExtension: "mp4", Category: "video", Size: 42, Starred: true, CreatedTime: "2026-03-02T00:00:00Z"}, {ID: "2", Kind: drive.KindFile, Name: "A_Clip.txt", FileExtension: "txt", Category: "document"}}
	if err := a.index().store.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	if err := b.index().store.SetMeta(index.MetaFullAt, ""); err != nil {
		t.Fatal(err)
	}
	signedOut := &accountSession{key: "signed-out", needsPassword: true}
	svc.accounts = append(svc.accounts, signedOut)
	page, err := svc.ListAllAccounts(ListRequest{Query: "_CLIP", Category: "video"}, "", nil)
	if err != nil || page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].Account != a.key {
		t.Fatalf("page %+v, error %v", page, err)
	}
	states := map[string]string{}
	for _, s := range page.Accounts {
		states[s.Account] = s.State
	}
	if states[a.key] != "ready" || states[b.key] != "index" || states[signedOut.key] != "signin" {
		t.Fatalf("statuses: %v", states)
	}
	query := &AIQuery{Under: "Media", Ext: []string{".MP4"}, MinSize: 40, Starred: "yes", CreatedAfter: "2026-03-01", Kind: "file", Sort: "created", Asc: true}
	page, err = svc.ListAllAccounts(ListRequest{}, "", query)
	if err != nil || page.Total != 1 || page.Entries[0].Entry.ID != "1" {
		t.Fatalf("advanced page %+v, error %v", page, err)
	}
	query.Aggregate = &AIAggregate{By: "folder"}
	if _, err = svc.ListAllAccounts(ListRequest{}, "", query); err == nil {
		t.Fatal("aggregate grouping accepted")
	}
	if _, err = svc.ListAllAccounts(ListRequest{}, "broken", nil); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

func TestAggregateCursorBindingAndClosedIndex(t *testing.T) {
	svc, a, b := aggregateFixture(t)
	rows := make([]index.Row, 220)
	for i := range rows {
		rows[i] = index.Row{ID: fmt.Sprintf("%03d", i), Name: "match", Kind: drive.KindFile}
	}
	if err := a.index().store.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	page, err := svc.ListAllAccounts(ListRequest{Sort: "name"}, "", nil)
	if err != nil || page.Next == "" {
		t.Fatalf("first page %+v: %v", page, err)
	}
	if _, err := svc.ListAllAccounts(ListRequest{Sort: "size"}, page.Next, nil); err == nil {
		t.Fatal("changed sort accepted")
	}
	if _, err := svc.ListAllAccounts(ListRequest{Sort: "name"}, page.Next, &AIQuery{}); err == nil {
		t.Fatal("changed query accepted")
	}
	if err := b.index().store.Close(); err != nil {
		t.Fatal(err)
	}
	page, err = svc.ListAllAccounts(ListRequest{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range page.Accounts {
		if status.Account == b.key && (status.State != "error" || status.Error == "") {
			t.Fatalf("closed index: %+v", status)
		}
	}
}

func TestAggregatePlayedSortAndAccountIsolation(t *testing.T) {
	svc, a, b := aggregateFixture(t)
	for _, s := range []*accountSession{a, b} {
		rows := make([]index.Row, 125)
		records := make([]playRecord, 125)
		for i := range rows {
			id := fmt.Sprintf("%03d", i)
			rows[i] = index.Row{ID: id, Name: id, Kind: drive.KindFile}
			records[i] = playRecord{FileID: id, PlayedAt: fmt.Sprintf("2026-01-%02dT00:00:00Z", i%28+1), Position: 50, Duration: 100}
		}
		if err := s.index().store.Upsert(rows); err != nil {
			t.Fatal(err)
		}
		// Seed a large history directly: opened() intentionally keeps only the newest 50.
		s.playLog().records = records
	}
	q := &AIQuery{Played: "unfinished", Sort: "played"}
	var got []AggregateEntry
	for cursor := ""; ; {
		page, err := svc.ListAllAccounts(ListRequest{}, cursor, q)
		if err != nil || page.Total != 250 {
			t.Fatalf("played page %+v: %v", page, err)
		}
		got = append(got, page.Entries...)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(got) != 250 {
		t.Fatalf("got %d played entries", len(got))
	}
	seen := map[string]bool{}
	for _, e := range got {
		key := e.Account + "/" + e.Entry.ID
		if seen[key] {
			t.Fatalf("duplicate %s", key)
		}
		seen[key] = true
	}
}
