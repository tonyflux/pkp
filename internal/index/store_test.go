package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func file(id, parent, name string, size int64) Row {
	return Row{ID: id, ParentID: parent, Kind: "drive#file", Name: name, Size: size,
		ModifiedTime: "2026-01-0" + id[len(id)-1:] + "T00:00:00Z", Category: "video", Phase: "PHASE_TYPE_COMPLETE"}
}

func folder(id, parent, name string) Row {
	return Row{ID: id, ParentID: parent, Kind: KindFolder, Name: name, Category: "folder",
		ModifiedTime: "2026-01-01T00:00:00Z"}
}

func ids(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func sameIDs(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUpsertRoundTripsAndOverwrites(t *testing.T) {
	s := openTemp(t)
	row := Row{ID: "f1", ParentID: "p", Kind: "drive#file", Name: "A.MKV", MimeType: "video/x-matroska",
		FileExtension: ".mkv", Size: 42, Starred: true, Phase: "PHASE_TYPE_COMPLETE", CreatedTime: "c",
		ModifiedTime: "m", ThumbnailLink: "https://t", Hash: "h", FolderType: "", Category: "video"}
	if err := s.Upsert([]Row{row}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.Get("f1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got != row {
		t.Errorf("get = %+v\nwant  %+v", got, row)
	}

	row.Name = "b.mkv"
	row.Starred = false
	row.Trashed = true
	if err := s.Upsert([]Row{row}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, _, _ = s.Get("f1")
	if got != row {
		t.Errorf("after overwrite = %+v\nwant  %+v", got, row)
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

func TestPatchTouchesOnlyGivenFields(t *testing.T) {
	s := openTemp(t)
	if err := s.Upsert([]Row{file("f1", "", "old", 1)}); err != nil {
		t.Fatal(err)
	}
	name := "New Name"
	if err := s.Patch("f1", Patch{Name: &name}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	got, _, _ := s.Get("f1")
	if got.Name != "New Name" || got.Starred || got.Trashed || got.Size != 1 {
		t.Errorf("after name patch = %+v", got)
	}
	// name_lower must follow, or search misses the renamed file.
	rows, _, err := s.Query(Query{Scope: ScopeSearch, Needle: "NEW"})
	if err != nil || len(rows) != 1 {
		t.Errorf("search after rename: rows=%d err=%v", len(rows), err)
	}

	yes := true
	if err := s.Patch("f1", Patch{Trashed: &yes}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Get("f1")
	if !got.Trashed || got.Name != "New Name" {
		t.Errorf("after trash patch = %+v", got)
	}
	if err := s.Patch("missing", Patch{Trashed: &yes}); err != nil {
		t.Errorf("patching an unknown id should be a no-op, got %v", err)
	}

	// Trashing a folder takes its contents with it, as one bin item;
	// restoring brings them all back.
	if err := s.Upsert([]Row{folder("d", "", "dir"), file("d1", "d", "one", 1), folder("dd", "d", "sub"), file("dd1", "dd", "deep", 1), file("x", "", "elsewhere", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Patch("d", Patch{Trashed: &yes}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"d", "d1", "dd", "dd1"} {
		if r, _, _ := s.Get(id); !r.Trashed {
			t.Errorf("%s should be trashed with its folder", id)
		}
	}
	if r, _, _ := s.Get("x"); r.Trashed {
		t.Error("unrelated rows stay")
	}
	bin, _, _ := s.Query(Query{Scope: ScopeTrash, Sort: "name", Asc: true})
	if !sameIDs(ids(bin), "d", "f1") {
		t.Errorf("bin = %v, want the folder as one item plus f1", ids(bin))
	}
	no := false
	if err := s.Patch("d", Patch{Trashed: &no}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"d", "d1", "dd", "dd1"} {
		if r, _, _ := s.Get(id); r.Trashed {
			t.Errorf("%s should be restored with its folder", id)
		}
	}
}

func TestQueryFolderOrdersFoldersFirstAndPaginates(t *testing.T) {
	s := openTemp(t)
	rows := []Row{
		file("f1", "", "zeta", 30),
		file("f2", "", "alpha", 10),
		folder("d3", "", "mid"),
		file("f4", "", "beta", 20),
		folder("d5", "", "aaa"),
		file("f6", "other", "elsewhere", 1),
	}
	trashed := file("f7", "", "gone", 5)
	trashed.Trashed = true
	rows = append(rows, trashed)
	if err := s.Upsert(rows); err != nil {
		t.Fatal(err)
	}

	got, total, err := s.Query(Query{Scope: ScopeFolder, ParentID: "", Sort: "name", Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (root, live)", total)
	}
	if !sameIDs(ids(got), "d5", "d3", "f2", "f4", "f1") {
		t.Errorf("name asc = %v", ids(got))
	}

	got, total, _ = s.Query(Query{Scope: ScopeFolder, Sort: "size", Asc: false, Limit: 2, Offset: 2})
	if total != 5 || !sameIDs(ids(got), "f1", "f4") {
		t.Errorf("size desc page 2 = %v total=%d", ids(got), total)
	}

	got, total, _ = s.Query(Query{Scope: ScopeFolder, Category: "other", Sort: "name", Asc: true})
	if total != 2 || !sameIDs(ids(got), "d5", "d3") {
		t.Errorf("other = %v total=%d (folders count as other, as in the UI)", ids(got), total)
	}
	got, _, _ = s.Query(Query{Scope: ScopeFolder, Category: "video", Sort: "modified", Asc: false})
	if !sameIDs(ids(got), "f4", "f2", "f1") {
		t.Errorf("video modified desc = %v", ids(got))
	}

	got, _, _ = s.Query(Query{Scope: ScopeTrash})
	if !sameIDs(ids(got), "f7") {
		t.Errorf("trash = %v", ids(got))
	}
}

func TestQuerySearchIsCaseAndScriptInsensitive(t *testing.T) {
	s := openTemp(t)
	rows := []Row{
		file("f1", "a", "電影 Movie.MKV", 1),
		file("f2", "b", "movie_two.mp4", 2),
		file("f3", "c", "unrelated", 3),
		file("f4", "d", "100%", 4),
	}
	rows[2].Starred = true
	if err := s.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	got, total, err := s.Query(Query{Scope: ScopeSearch, Needle: "MOVIE", Sort: "name", Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || !sameIDs(ids(got), "f2", "f1") {
		t.Errorf("MOVIE = %v total=%d", ids(got), total)
	}
	got, _, _ = s.Query(Query{Scope: ScopeSearch, Needle: "電影"})
	if !sameIDs(ids(got), "f1") {
		t.Errorf("Movies = %v", ids(got))
	}
	got, _, _ = s.Query(Query{Scope: ScopeSearch, Needle: "%"})
	if !sameIDs(ids(got), "f4") {
		t.Errorf("literal %% = %v, LIKE wildcards must be escaped", ids(got))
	}
	if _, _, err := s.Query(Query{Scope: ScopeSearch, Needle: "  "}); err == nil {
		t.Error("blank needle should be rejected")
	}
	got, _, _ = s.Query(Query{Scope: ScopeStarred})
	if !sameIDs(ids(got), "f3") {
		t.Errorf("starred = %v", ids(got))
	}
}

func TestQueryFilesSpansTheDriveWithoutFolders(t *testing.T) {
	s := openTemp(t)
	doc := file("f3", "d1", "notes.pdf", 3)
	doc.Category = "document"
	inBin := file("f5", "d2", "binned clip", 5)
	inBin.Trashed = true
	binDir := folder("d2", "", "binned")
	binDir.Trashed = true
	rows := []Row{
		folder("d1", "", "deep"),
		file("f1", "", "root clip", 1),
		file("f2", "d1", "Deep Clip", 2),
		doc,
		file("f4", "x", "orphan clip", 4), // parent unknown to the index
		binDir, inBin,
	}
	if err := s.Upsert(rows); err != nil {
		t.Fatal(err)
	}

	got, total, err := s.Query(Query{Scope: ScopeFiles, Sort: "modified"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || !sameIDs(ids(got), "f4", "f3", "f2", "f1") {
		t.Errorf("files = %v total=%d, want every live file of every folder, newest first, no folders", ids(got), total)
	}
	got, total, _ = s.Query(Query{Scope: ScopeFiles, Category: "video", Sort: "name", Asc: true, Limit: 2, Offset: 1})
	if total != 3 || !sameIDs(ids(got), "f4", "f1") {
		t.Errorf("video name asc page 2 = %v total=%d", ids(got), total)
	}
	got, _, _ = s.Query(Query{Scope: ScopeFiles, Category: "other"})
	if len(got) != 0 {
		t.Errorf("other = %v, folders must not count as other here", ids(got))
	}
	got, _, _ = s.Query(Query{Scope: ScopeFiles, Needle: "CLIP", Sort: "size", Asc: true})
	if !sameIDs(ids(got), "f1", "f2", "f4") {
		t.Errorf("needle = %v", ids(got))
	}
}

// Audio has its own chip, so Other no longer takes audio files.
func TestQueryAudioIsNotOther(t *testing.T) {
	s := openTemp(t)
	song := file("f1", "", "song.flac", 1)
	song.Category = "audio"
	zip := file("f2", "", "pack.zip", 2)
	zip.Category = "archive"
	if err := s.Upsert([]Row{song, zip}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{ScopeFolder, ScopeFiles} {
		got, _, _ := s.Query(Query{Scope: scope, Category: "audio"})
		if !sameIDs(ids(got), "f1") {
			t.Errorf("%s audio = %v", scope, ids(got))
		}
		got, _, _ = s.Query(Query{Scope: scope, Category: "other"})
		if !sameIDs(ids(got), "f2") {
			t.Errorf("%s other = %v, want the archive alone", scope, ids(got))
		}
	}
}

func TestDeleteTrashedNotInDropsDestroyedTopLevelWithDescendants(t *testing.T) {
	s := openTemp(t)
	dir := folder("t1", "", "gone-dir")
	dir.Trashed, dir.DeleteTime = true, "P1"
	child := file("t1c", "t1", "inside", 1)
	child.Trashed, child.DeleteTime = true, "P1" // trashed along with the folder
	own := file("t1o", "t1", "trashed-first", 1)
	own.Trashed, own.DeleteTime = true, "P0" // trashed on its own, before the folder
	b := file("t2", "", "b", 2)
	b.Trashed, b.DeleteTime = true, "P2"
	if err := s.Upsert([]Row{dir, child, own, b, file("f3", "", "live", 3)}); err != nil {
		t.Fatal(err)
	}

	// The bin shows what was trashed in its own right: not the folder's
	// contents, but a file trashed earlier inside it, as the server does.
	rows, total, err := s.Query(Query{Scope: ScopeTrash, Sort: "name", Asc: true})
	if err != nil || total != 3 || !sameIDs(ids(rows), "t1", "t2", "t1o") {
		t.Errorf("trash view = %v total=%d err=%v", ids(rows), total, err)
	}

	n, err := s.DeleteTrashedNotIn([]string{"t2", "t1o"})
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v, want 3 (folder + both children, destroyed with it)", n, err)
	}
	for _, id := range []string{"t1", "t1c", "t1o"} {
		if _, ok, _ := s.Get(id); ok {
			t.Errorf("%s should be gone", id)
		}
	}
	if _, ok, _ := s.Get("t2"); !ok {
		t.Error("t2 should stay")
	}
	if _, ok, _ := s.Get("f3"); !ok {
		t.Error("live rows are not touched")
	}

	// A full walk that saw only f3 leaves only f3, and its record with it.
	f3, _, _ := s.Get("f3")
	if err := s.BeginWalk(); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWalk([]Row{f3}); err != nil {
		t.Fatal(err)
	}
	n, err = s.FinishWalk("2026-10-17T00:00:00Z")
	if err != nil || n != 1 {
		t.Fatalf("FinishWalk n=%d err=%v", n, err)
	}
	if c, _ := s.Count(); c != 1 {
		t.Errorf("count = %d, want 1", c)
	}
	if seen, _ := s.WalkSeen(); seen != 0 {
		t.Errorf("walk_seen keeps %d rows after the walk", seen)
	}
	if at, _ := s.Meta(MetaFullAt); at != "2026-10-17T00:00:00Z" {
		t.Errorf("full_at = %q", at)
	}
}

func TestPathStopsAtBrokenChain(t *testing.T) {
	s := openTemp(t)
	if err := s.Upsert([]Row{
		folder("root1", "", "A"),
		folder("mid", "root1", "B"),
		folder("leaf", "mid", "C"),
		folder("orphan", "missing", "D"),
	}); err != nil {
		t.Fatal(err)
	}
	path, err := s.Path("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 || path[0].Name != "A" || path[1].Name != "B" || path[2].Name != "C" {
		t.Errorf("path = %+v", path)
	}
	path, err = s.Path("orphan")
	if err != nil || len(path) != 1 || path[0].ID != "orphan" {
		t.Errorf("orphan path = %+v err=%v", path, err)
	}
	path, err = s.Path("")
	if err != nil || len(path) != 0 {
		t.Errorf("root path = %+v err=%v", path, err)
	}
}

func TestOpenRebuildsOnSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert([]Row{file("f1", "", "x", 1)}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{MetaCursor, "T"}, {MetaAccount, "acct"}, {MetaSchema, "0"}} {
		if err := s.SetMeta(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if n, _ := s.Count(); n != 0 {
		t.Errorf("count after schema bump = %d, want 0", n)
	}
	if v, _ := s.Meta(MetaCursor); v != "" {
		t.Errorf("cursor survived the rebuild: %q", v)
	}
	if v, _ := s.Meta(MetaSchema); v != schemaVersion {
		t.Errorf("schema = %q", v)
	}

	// Same schema: reopening keeps everything.
	if err := s.Upsert([]Row{file("f2", "", "y", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(MetaCursor, "T2"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Meta(MetaCursor); v != "T2" {
		t.Errorf("cursor = %q after plain reopen", v)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Meta(MetaCursor); v != "" {
		t.Error("Clear must forget the cursor")
	}
	if v, _ := s.Meta(MetaSchema); v != schemaVersion {
		t.Error("Clear must keep the schema marker")
	}
}

// The sums count the recycle bin, because the server's usage does, and
// leave folders out: a folder's size is not bytes of its own.
func TestUsageByCategoryIncludesTrashAndSkipsFolders(t *testing.T) {
	s := openTemp(t)
	trashed := file("f3", "", "old.mkv", 300)
	trashed.Trashed = true
	img := file("f4", "", "a.png", 40)
	img.Category = "image"
	dir := folder("d1", "", "dir")
	dir.Size = 9999
	if err := s.Upsert([]Row{file("f1", "", "a.mkv", 100), file("f2", "d1", "b.mkv", 20), trashed, img, dir}); err != nil {
		t.Fatal(err)
	}
	sums, err := s.UsageByCategory()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums["video"] != 420 || sums["image"] != 40 {
		t.Errorf("sums = %v, want video 420 and image 40 only", sums)
	}
}

func TestSubtreeReadsOneMorePastTheLimit(t *testing.T) {
	s := openTemp(t)
	gone := file("t1", "d1", "gone", 1)
	gone.Trashed = true
	rows := []Row{
		folder("d1", "", "top"), folder("d2", "d1", "mid"), folder("d3", "d2", "low"),
		file("f1", "d1", "a", 1), file("f2", "d2", "b", 1), file("f3", "d3", "c", 1), gone,
		file("x1", "", "outside", 1),
		// A parent cycle in a corrupt index must not loop.
		folder("c1", "c2", "loop-a"), folder("c2", "c1", "loop-b"),
	}
	if err := s.Upsert(rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.Subtree("d1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := set("d2", "d3", "f1", "f2", "f3"); !sameSet(ids(got), want) {
		t.Errorf("subtree = %v, want %v (live, any depth, not the root)", ids(got), want)
	}
	if got, _ := s.Subtree("d1", 2); len(got) != 3 {
		t.Errorf("limit 2 read %d rows, want 3 (limit+1)", len(got))
	}
	if got, _ := s.Subtree("d1", 5); len(got) != 5 {
		t.Errorf("exactly the limit read %d rows, want 5", len(got))
	}
	if got, err := s.Subtree("c1", 100); err != nil || !sameSet(ids(got), set("c1", "c2")) {
		t.Errorf("cycle = %v err=%v", ids(got), err)
	}
	if got, _ := s.Subtree("", 100); len(got) != 7 {
		t.Errorf("root subtree = %v, want the 7 live rows reachable from the root", ids(got))
	}
}

func set(ids ...string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func sameSet(got []string, want map[string]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for _, id := range got {
		if !want[id] {
			return false
		}
	}
	return true
}

// searchRows is a small drive in the server's own stamp format (+08:00).
func searchRows(t *testing.T) *Store {
	t.Helper()
	s := openTemp(t)
	at := func(r Row, created, modified string) Row {
		r.CreatedTime, r.ModifiedTime = created, modified
		return r
	}
	ocean := at(file("v1", "dB", "Ocean_Planet.MKV", 3000), "2026-03-10T00:30:00.123+08:00", "2026-03-12T10:00:00+08:00")
	ocean.FileExtension, ocean.Starred = ".mkv", true
	wild := at(file("v2", "dA", "100% wild.mp4", 500), "2026-04-01T12:00:00Z", "2026-04-02T12:00:00Z")
	nights := at(file("v3", "", "1000 nights.avi", 800), "2026-05-01T12:00:00+08:00", "2026-05-01T12:00:00+08:00")
	notes := at(file("n1", "", "notes_v2.txt", 10), "2025-12-31T23:30:00Z", "2026-01-01T00:00:00Z")
	notes.FileExtension, notes.Category = "txt", "document"
	zip := at(file("z1", "", "archive.zip", 70000), "2026-02-01T00:00:00Z", "2026-02-01T00:00:00Z")
	zip.Category = "archive"
	trashed := at(file("t1", "dB", "ocean trash.mkv", 9), "2026-03-10T12:00:00Z", "2026-03-10T12:00:00Z")
	trashed.Trashed = true
	if err := s.Upsert([]Row{
		folder("dA", "", "影视"), folder("dB", "dA", "纪录片"), folder("fo", "", "Ocean Folder"),
		ocean, wild, nights, notes, zip, trashed,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSearchFiltersLiveRows(t *testing.T) {
	s := searchRows(t)
	cst := time.FixedZone("CST", 8*3600)
	cases := []struct {
		name string
		q    Search
		want map[string]bool
	}{
		{"keywords OR, live only", Search{Keywords: []string{"OCEAN", " wild "}}, set("v1", "v2", "fo")},
		{"% is literal", Search{Keywords: []string{"100%"}}, set("v2")},
		{"_ is literal", Search{Keywords: []string{"_"}}, set("v1", "n1")},
		{"blank keywords match all", Search{Keywords: []string{" "}, Kind: "file"}, set("v1", "v2", "v3", "n1", "z1")},
		{"other is files outside the three", Search{Category: "other"}, set("z1")},
		{"video", Search{Category: "video"}, set("v1", "v2", "v3")},
		{"ext, name suffix standing in", Search{Exts: []string{"mp4", "txt"}}, set("v2", "n1")},
		{"ext with dot stored", Search{Exts: []string{"mkv"}}, set("v1")},
		{"size bounds inclusive", Search{MinSize: 500, MaxSize: 3000}, set("v1", "v2", "v3")},
		{"min size only", Search{MinSize: 3001}, set("z1")},
		// v1 was created 2026-03-09T16:30Z. Compared as text, "2026-03-10T00:30…"
		// would sit after the bound below and wrongly match.
		{"created bounds by instant", Search{CreatedFrom: time.Date(2026, 3, 9, 17, 0, 0, 0, time.UTC)}, set("v2", "v3")},
		{"created local day", Search{CreatedFrom: time.Date(2026, 3, 10, 0, 0, 0, 0, cst), CreatedBefore: time.Date(2026, 3, 11, 0, 0, 0, 0, cst)}, set("v1")},
		{"modified before", Search{ModifiedBefore: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Kind: "file"}, set("n1")},
		{"modified from, folders' stamps too", Search{ModifiedFrom: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}, set("v1", "v2", "v3")},
		{"kind folder", Search{Kind: "folder"}, set("dA", "dB", "fo")},
		{"starred yes", Search{Starred: "yes"}, set("v1")},
		{"starred no", Search{Starred: "no", Kind: "file"}, set("v2", "v3", "n1", "z1")},
		{"ids", Search{IDs: []string{"v1", "t1", "zz"}}, set("v1")},
		{"empty id set", Search{IDs: []string{}}, set()},
		{"under, any depth, root left out", Search{Under: []string{"dA"}}, set("dB", "v1", "v2")},
		{"under several roots", Search{Under: []string{"dB", "fo"}}, set("v1")},
		{"under a file", Search{Under: []string{"v1"}}, set()},
		{"combined", Search{Keywords: []string{"ocean", "wild"}, Category: "video", Under: []string{"dA"}, Starred: "no"}, set("v2")},
	}
	for _, c := range cases {
		got, total, err := s.Search(c.q)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !sameSet(ids(got), c.want) || total != len(c.want) {
			t.Errorf("%s: got %v total %d, want %v", c.name, ids(got), total, c.want)
		}
	}
}

func TestSearchSortsLimitsAndRefusesUnknownValues(t *testing.T) {
	s := searchRows(t)
	got, total, err := s.Search(Search{Kind: "file", Sort: "size", Asc: true, Limit: 2})
	if err != nil || total != 5 || !sameIDs(ids(got), "n1", "v2") {
		t.Errorf("size asc = %v total=%d err=%v", ids(got), total, err)
	}
	got, _, _ = s.Search(Search{Kind: "file", Sort: "created"})
	if !sameIDs(ids(got), "v3", "v2", "v1", "z1", "n1") {
		t.Errorf("created desc = %v", ids(got))
	}
	// "100%" sorts before "1000": '%' is below '0'.
	got, _, _ = s.Search(Search{Kind: "file", Sort: "name", Asc: true})
	if !sameIDs(ids(got), "v2", "v3", "z1", "n1", "v1") {
		t.Errorf("name asc = %v", ids(got))
	}
	// Equal keys fall back to the id, so pages never shuffle.
	got, _, _ = s.Search(Search{Kind: "folder", Sort: "size"})
	if !sameIDs(ids(got), "dA", "dB", "fo") {
		t.Errorf("tie = %v, want by id", ids(got))
	}
	for _, q := range []Search{{Sort: "played"}, {Category: "unsupported"}, {Kind: "link"}, {Starred: "maybe"}} {
		if _, _, err := s.Search(q); err == nil {
			t.Errorf("%+v should be refused", q)
		}
	}
}

func TestAggregateGroupsFiles(t *testing.T) {
	s := searchRows(t)
	groups, total, err := s.Aggregate(Search{}, "folder", "size", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Group{{"", 70810, 3}, {"dB", 3000, 1}, {"dA", 500, 1}}
	if total != 5 || fmt.Sprint(groups) != fmt.Sprint(want) {
		t.Errorf("by folder = %v total %d, want %v total 5", groups, total, want)
	}
	groups, _, _ = s.Aggregate(Search{}, "category", "count", 10)
	if want := []Group{{"video", 4300, 3}, {"other", 70000, 1}, {"document", 10, 1}}; fmt.Sprint(groups) != fmt.Sprint(want) {
		t.Errorf("by category = %v, want %v", groups, want)
	}
	groups, total, _ = s.Aggregate(Search{Category: "video"}, "ext", "size", 2)
	if want := []Group{{"mkv", 3000, 1}, {"avi", 800, 1}}; total != 3 || fmt.Sprint(groups) != fmt.Sprint(want) {
		t.Errorf("video by ext = %v total %d, want %v total 3", groups, total, want)
	}
	groups, _, _ = s.Aggregate(Search{Under: []string{"dA"}}, "folder", "count", 10)
	if want := []Group{{"dB", 3000, 1}, {"dA", 500, 1}}; fmt.Sprint(groups) != fmt.Sprint(want) {
		t.Errorf("under dA = %v, want %v (count ties broken by size)", groups, want)
	}
	for _, bad := range [][2]string{{"name", "size"}, {"folder", "bytes"}} {
		if _, _, err := s.Aggregate(Search{}, bad[0], bad[1], 10); err == nil {
			t.Errorf("%v should be refused", bad)
		}
	}
}

func TestFoldersAtAndNamed(t *testing.T) {
	s := openTemp(t)
	gone := folder("g", "", "纪录片 old")
	gone.Trashed = true
	if err := s.Upsert([]Row{
		folder("m1", "", "Movies"), folder("m2", "", "movies"), folder("x", "", "Other"),
		folder("d1", "m1", "纪录片"), folder("d2", "m2", "纪录片"), folder("d3", "x", "纪录片 4K"),
		file("f1", "m1", "纪录片", 1), gone,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.FoldersAt([]string{"MOVIES", "纪录片"})
	if err != nil || !sameIDs(ids(got), "d1", "d2") {
		t.Errorf("path = %v err=%v, want both same-named branches, folders only", ids(got), err)
	}
	if got, _ := s.FoldersAt([]string{"Movies", "nope"}); got != nil {
		t.Errorf("missing level = %v, want nil", ids(got))
	}
	if got, _ := s.FoldersAt([]string{"纪录片"}); got != nil {
		t.Errorf("not at the root = %v, want nil", ids(got))
	}
	if got, _ := s.FoldersAt(nil); got != nil {
		t.Errorf("no segments = %v", ids(got))
	}
	got, _ = s.FoldersNamed("纪录片", false, 10)
	if !sameIDs(ids(got), "d1", "d2") {
		t.Errorf("named = %v", ids(got))
	}
	got, _ = s.FoldersNamed("纪录", true, 10)
	if !sameIDs(ids(got), "d1", "d2", "d3") {
		t.Errorf("containing = %v, want shorter names first, trashed left out", ids(got))
	}
	if got, _ := s.FoldersNamed("%", true, 10); len(got) != 0 {
		t.Errorf("%% matched %v", ids(got))
	}
}
