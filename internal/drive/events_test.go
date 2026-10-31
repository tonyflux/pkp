package drive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// A play event as /drive/v1/events serves it: seconds are quoted strings,
// times carry the server's +08:00 zone, the file rides along whole.
const playEventsBody = `{"events":[
 {"id":"ev1","type":"TYPE_PLAY","file_id":"f1","file_name":"movie.mkv","mime_type":"video/x-matroska",
  "updated_time":"2026-10-12T21:04:05.123+08:00","created_time":"2026-10-01T10:00:00+08:00",
  "params":{"play_duration":"7606","play_seconds":"6048"},
  "reference_resource":{"kind":"drive#file","id":"f1","name":"movie.mkv","mime_type":"video/x-matroska",
   "thumbnail_link":"https://thumb/f1","size":"5713293548","phase":"PHASE_TYPE_COMPLETE",
   "params":{"duration":"7606","width":"1920","height":"1080"}}},
 {"id":"ev2","type":"TYPE_PLAY","file_id":"f2","file_name":"gone.mp4",
  "updated_time":"2026-10-11T08:00:00+08:00","params":{"play_seconds":"12"}},
 {"id":"ev3","type":"TYPE_PLAY","file_id":"f3","file_name":"clip.mp4",
  "updated_time":"2026-10-10T08:00:00+08:00","params":{"play_seconds":"30.5","play_duration":""},
  "reference_resource":{"kind":"drive#file","id":"f3","name":"clip.mp4","trashed":true,"params":{"duration":"120"}}}
],"next_page_token":"next"}`

func TestListEventsEncodesFilterAndDecodesStringSeconds(t *testing.T) {
	var query map[string]string
	var path string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var err error
		if query, err = parseQuery(r.URL.RawQuery); err != nil {
			t.Errorf("parse query: %v", err)
		}
		io.WriteString(w, playEventsBody)
	}, newStaticTokens("tok"))

	list, err := client.ListEvents(context.Background(), ListEventsParams{
		Type: EventTypePlay, Limit: 20, ThumbnailSize: ThumbnailMedium, PageToken: "p1",
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	if path != "/drive/v1/events" {
		t.Errorf("path = %q", path)
	}
	var filters map[string]map[string]any
	if err := json.Unmarshal([]byte(query["filters"]), &filters); err != nil {
		t.Fatalf("filters %q: %v", query["filters"], err)
	}
	if filters["type"]["in"] != EventTypePlay {
		t.Errorf("filters = %v, want type in TYPE_PLAY", filters)
	}
	if query["limit"] != "20" || query["thumbnail_size"] != "SIZE_MEDIUM" || query["page_token"] != "p1" {
		t.Errorf("query = %v", query)
	}

	if list.NextPageToken != "next" || len(list.Events) != 3 {
		t.Fatalf("list = %+v", list)
	}
	first := list.Events[0]
	if first.FileID != "f1" || first.FileName != "movie.mkv" || first.UpdatedTime != "2026-10-12T21:04:05.123+08:00" {
		t.Errorf("first = %+v", first)
	}
	if pos, dur := first.Played(); pos != 6048 || dur != 7606 {
		t.Errorf("first played = %v/%v, want 6048/7606", pos, dur)
	}
	ref := first.Reference
	if ref == nil || ref.ThumbnailLink != "https://thumb/f1" || ref.Size != 5713293548 || ref.Trashed {
		t.Errorf("first reference = %+v", ref)
	}

	if gone := list.Events[1]; gone.Reference != nil {
		t.Errorf("an event without reference_resource must decode to nil, got %+v", gone.Reference)
	}
	trashed := list.Events[2]
	if trashed.Reference == nil || !trashed.Reference.Trashed {
		t.Errorf("trashed reference = %+v", trashed.Reference)
	}
	// An empty play_duration falls back to the file's own runtime.
	if pos, dur := trashed.Played(); pos != 30.5 || dur != 120 {
		t.Errorf("trashed played = %v/%v, want 30.5/120", pos, dur)
	}
}

func TestSecondsAcceptsBothEncodings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Seconds
	}{
		{`"6048"`, 6048},
		{`6048`, 6048},
		{`"12.5"`, 12.5},
		{`""`, 0},
		{`null`, 0},
	} {
		var s Seconds
		if err := json.Unmarshal([]byte(tc.in), &s); err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if s != tc.want {
			t.Errorf("%s = %v, want %v", tc.in, s, tc.want)
		}
	}
	var s Seconds
	if err := json.Unmarshal([]byte(`"soon"`), &s); err == nil {
		t.Error("a non-numeric string must be an error")
	}
}
