package stream

import (
	"io"
	"net/http"
	"testing"
)

func TestReaderCrossOriginRangeAndRevocation(t *testing.T) {
	origin := upstream(t, payload(128), nil)
	p := proxy(t)
	handle, err := p.Publish(Source{URL: origin.URL})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodOptions, handle.URL, nil)
	req.Header.Set("Origin", "wails://wails.localhost")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "range")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Headers") == "" {
		t.Fatal("reader preflight refused")
	}
	req, _ = http.NewRequest(http.MethodGet, handle.URL, nil)
	req.Header.Set("Origin", "wails://wails.localhost")
	req.Header.Set("Range", "bytes=4-7")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(data) != 4 || resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("Access-Control-Expose-Headers") == "" {
		t.Fatal("reader cannot access ranged content")
	}
	p.Unpublish(handle.ID)
	resp, err = http.Get(handle.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("revoked reading URL still accessible")
	}
}
