package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/netproxy"
)

// ListModels returns the service's model IDs without calling a model. It needs
// the saved connection and key, but no selected model, and never retries.
func ListModels(ctx context.Context, cfg Config) ([]string, error) {
	base, err := cfg.connectionEndpoint(false)
	if err != nil {
		return nil, err
	}
	endpoint := base + "/models"
	header := cfg.openAIHeader()
	if cfg.Provider == Anthropic {
		endpoint = base + "/v1/models"
		header = cfg.anthropicHeader()
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, cfg.fail(KindConfig, 0, i18n.F("服务地址无效：%v", endpoint))
	}
	ctx, cancel := context.WithTimeout(ctx, TestTimeout)
	defer cancel()
	client := cfg.HTTP
	if client == nil {
		client = netproxy.Client()
	}
	// Copy rather than changing the shared proxy client or a caller's client.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ids := make(map[string]struct{})
	cursors := make(map[string]struct{})
	remaining := maxResponse
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, cfg.fail(KindConfig, 0, i18n.F("服务地址无效：%v", err))
		}
		req.Header = header.Clone()
		resp, err := bounded.Do(req)
		if err != nil {
			return nil, cfg.noAnswer(ctx, err)
		}
		status, data, err := cfg.readAll(ctx, resp)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if status/100 != 2 {
			return nil, cfg.refused(status, data)
		}
		remaining -= len(data)
		if remaining <= 0 {
			return nil, cfg.fail(KindInvalid, 0, i18n.F("回答里没有内容：%s", data))
		}
		var page struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore *bool  `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, cfg.fail(KindInvalid, 0, i18n.F("服务返回的不是 JSON：%s", data))
		}
		if page.Data == nil || (cfg.Provider == Anthropic && page.HasMore == nil) {
			return nil, cfg.fail(KindInvalid, 0, i18n.F("回答里没有内容：%s", data))
		}
		for _, model := range page.Data {
			if strings.TrimSpace(model.ID) == "" || (cfg.APIKey != "" && strings.Contains(model.ID, cfg.APIKey)) {
				return nil, cfg.fail(KindInvalid, 0, i18n.F("回答里没有内容：%s", data))
			}
			ids[model.ID] = struct{}{}
		}
		if cfg.Provider != Anthropic || !*page.HasMore {
			break
		}
		if _, seen := cursors[page.LastID]; seen || strings.TrimSpace(page.LastID) == "" || len(page.Data) == 0 || (cfg.APIKey != "" && strings.Contains(page.LastID, cfg.APIKey)) {
			return nil, cfg.fail(KindInvalid, 0, i18n.F("回答里没有内容：%s", data))
		}
		cursors[page.LastID] = struct{}{}
		// Only the opaque cursor is accepted; server-provided URLs are ignored.
		query := url.Values{"after_id": {page.LastID}}
		u.RawQuery = query.Encode()
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
