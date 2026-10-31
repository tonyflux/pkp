package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Source values represented by the subtitle response model. The client
// selects SubtitleExtracted and ignores other source values.
const (
	SubtitleUploaded = 0
	// SubtitleExtracted is the source value selected for transcode playback.
	SubtitleExtracted = 1
	SubtitleSpeech    = 2
)

// Subtitle is one entry returned by the subtitle endpoint.
type Subtitle struct {
	GCID string `json:"gcid"`
	URL  string `json:"url"`
	// Ext is the format, without the dot: "srt", "ass", "ssa"…
	Ext  string `json:"ext"`
	Name string `json:"name"`
	// Duration is how long the subtitle runs, in milliseconds.
	Duration int64 `json:"duration"`
	// Languages are as the source named them: a code ("eng"), a code with
	// a variant ("chiSimplified", "korSDH") or the track's title ("Simplified Chinese Subtitles").
	Languages []string `json:"languages"`
	Source    int      `json:"source"`
	// Score is the score returned with this entry.
	Score int64 `json:"score"`
}

// Subtitles requests entries for gcid (File.Hash), preserving response order.
// A positive duration is sent in seconds; otherwise it is omitted. The
// request does not send a file name.
func (c *Client) Subtitles(ctx context.Context, gcid string, duration int64) ([]Subtitle, error) {
	if gcid == "" {
		return nil, errors.New("drive: subtitles need the video's gcid")
	}
	query := url.Values{"gcid": {gcid}}
	if duration > 0 {
		query.Set("duration", strconv.FormatInt(duration, 10))
	}
	var out struct {
		Code   int        `json:"code"`
		Result string     `json:"result"`
		Data   []Subtitle `json:"data"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, path: "/oracle/subtitle", query: query}, &out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("drive: GET /oracle/subtitle: code %d: %s", out.Code, out.Result)
	}
	return out.Data, nil
}
