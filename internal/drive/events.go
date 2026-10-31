package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// EventTypePlay is the activity the official clients log when a video is
// watched: one event per file, moved to the top whenever it is played again.
const EventTypePlay = "TYPE_PLAY"

// Seconds decodes a duration in seconds that the API sends as a quoted
// string ("6048") or, defensively, as a bare number. An empty string is 0.
type Seconds float64

func (s *Seconds) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*s = 0
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		data = []byte(text)
	}
	if len(data) == 0 {
		*s = 0
		return nil
	}
	n, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return fmt.Errorf("drive: seconds %q: %w", data, err)
	}
	*s = Seconds(n)
	return nil
}

// EventParams are the figures a play event carries.
type EventParams struct {
	// PlaySeconds is where playback was left, PlayDuration the runtime.
	PlaySeconds  Seconds `json:"play_seconds"`
	PlayDuration Seconds `json:"play_duration"`
}

// Event is one entry of the account's activity log. Only the fields this
// client uses are modelled.
type Event struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	// UpdatedTime is when the event last happened, RFC 3339 in the
	// server's zone (+08:00); CreatedTime is its first occurrence.
	CreatedTime string      `json:"created_time"`
	UpdatedTime string      `json:"updated_time"`
	Params      EventParams `json:"params"`
	// Reference is the file as it is now, nil when it no longer exists.
	// It is the file's own record: Trashed says it went to the bin.
	Reference *File `json:"reference_resource"`
}

// Played reports where playback was left and the runtime, both in seconds.
// The runtime falls back to the file's own media duration when the event
// did not record one.
func (e Event) Played() (position, duration float64) {
	position, duration = float64(e.Params.PlaySeconds), float64(e.Params.PlayDuration)
	if duration <= 0 && e.Reference != nil {
		if d, err := strconv.ParseFloat(e.Reference.Params["duration"], 64); err == nil {
			duration = d
		}
	}
	return position, duration
}

// EventList is one page of ListEvents, newest first.
type EventList struct {
	Events        []Event `json:"events"`
	NextPageToken string  `json:"next_page_token"`
}

// ListEventsParams selects a page of the activity log.
type ListEventsParams struct {
	// Type narrows the log to one event type, e.g. EventTypePlay; empty
	// returns every type.
	Type      string
	PageToken string
	// Limit is left to the server when zero.
	Limit int
	// ThumbnailSize selects the thumbnail variant of each reference.
	ThumbnailSize ThumbnailSize
}

// ListEvents returns one page of the activity log. It only reads: the log
// is written by the clients that play, and this one does not report its
// own plays.
func (c *Client) ListEvents(ctx context.Context, params ListEventsParams) (*EventList, error) {
	query := url.Values{}
	if params.Type != "" {
		filters, err := NewFilters().Set("type", "in", params.Type).encode()
		if err != nil {
			return nil, err
		}
		query.Set("filters", filters)
	}
	if params.Limit > 0 {
		query.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.ThumbnailSize != "" {
		query.Set("thumbnail_size", string(params.ThumbnailSize))
	}
	if params.PageToken != "" {
		query.Set("page_token", params.PageToken)
	}

	var out EventList
	if err := c.do(ctx, request{method: http.MethodGet, path: "/drive/v1/events", query: query}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
