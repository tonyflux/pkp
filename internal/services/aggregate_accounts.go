package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/index"
)

// AggregateEntry associates an indexed file with the account that owns it.
type AggregateEntry struct {
	Account string `json:"account"`
	Entry   Entry  `json:"entry"`
}

// AggregateAccount describes whether an account contributed to the page.
type AggregateAccount struct {
	Account string `json:"account"`
	State   string `json:"state"`
	Error   string `json:"error"`
}

// AggregatePage is a globally ordered page of files from ready accounts.
type AggregatePage struct {
	Entries  []AggregateEntry   `json:"entries"`
	Accounts []AggregateAccount `json:"accounts"`
	Total    int                `json:"total"`
	Next     string             `json:"next"`
}

type aggregateCursor struct {
	Version  int            `json:"v"`
	Query    string         `json:"q"`
	Category string         `json:"c"`
	Sort     string         `json:"s"`
	Asc      bool           `json:"a"`
	Advanced string         `json:"advanced"`
	Accounts []string       `json:"accounts"`
	Offsets  map[string]int `json:"offsets"`
}

type aggregateSource struct {
	account string
	store   *index.Store
	rows    []index.Row
	total   int
	plays   map[string]RecentPlay
	used    int
}

// ListAllAccounts lists live files without switching the active account.
func (d *DriveService) ListAllAccounts(req ListRequest, cursor string, query *AIQuery) (AggregatePage, error) {
	q := index.Query{Scope: index.ScopeFiles, Needle: strings.TrimSpace(req.Query), Category: req.Category, Sort: req.Sort, Asc: req.Asc, Limit: listPageSize}
	if q.Category == "all" {
		q.Category = ""
	}
	if q.Sort != "name" && q.Sort != "size" {
		q.Sort = "modified"
	}
	var advanced *AIQuery
	var binding string
	if query != nil {
		if query.Aggregate != nil {
			return AggregatePage{}, errors.New("aggregate grouping is unsupported across accounts")
		}
		cleaned := cleanQuery(*query)
		if cleaned.Sort == "" {
			cleaned.Sort, cleaned.Asc = q.Sort, q.Asc
		}
		cleaned.Limit = listPageSize
		advanced = &cleaned
		q.Sort, q.Asc = cleaned.Sort, cleaned.Asc
		data, err := json.Marshal(cleaned)
		if err != nil {
			return AggregatePage{}, err
		}
		binding = string(data)
	}
	sessions := d.sessions()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].key < sessions[j].key })
	keys := make([]string, len(sessions))
	for i, s := range sessions {
		keys[i] = s.key
	}
	c := aggregateCursor{Version: 1, Query: q.Needle, Category: q.Category, Sort: q.Sort, Asc: q.Asc, Advanced: binding, Accounts: keys, Offsets: map[string]int{}}
	if cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return AggregatePage{}, errors.New("invalid aggregate cursor")
		}
		var saved aggregateCursor
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&saved); err != nil {
			return AggregatePage{}, errors.New("invalid aggregate cursor")
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return AggregatePage{}, errors.New("invalid aggregate cursor")
		}
		if saved.Version != 1 || saved.Query != c.Query || saved.Category != c.Category || saved.Sort != c.Sort || saved.Asc != c.Asc || saved.Advanced != c.Advanced || len(saved.Accounts) != len(keys) || saved.Offsets == nil {
			return AggregatePage{}, errors.New("stale aggregate cursor")
		}
		for i, key := range keys {
			if saved.Accounts[i] != key {
				return AggregatePage{}, errors.New("stale aggregate cursor")
			}
		}
		consumed := 0
		for key, n := range saved.Offsets {
			if n < 0 || n > int(^uint(0)>>1)-consumed {
				return AggregatePage{}, errors.New("invalid aggregate cursor")
			}
			i := sort.SearchStrings(keys, key)
			if i == len(keys) || keys[i] != key {
				return AggregatePage{}, errors.New("invalid aggregate cursor")
			}
			consumed += n
		}
		if consumed <= 0 || consumed%listPageSize != 0 {
			return AggregatePage{}, errors.New("invalid aggregate cursor")
		}
		c = saved
	}
	page := AggregatePage{Entries: []AggregateEntry{}, Accounts: make([]AggregateAccount, 0, len(sessions))}
	sources := make([]aggregateSource, 0, len(sessions))
	for _, s := range sessions {
		status := AggregateAccount{Account: s.key}
		if _, err := s.api(); err != nil {
			status.State, status.Error = "signin", err.Error()
		} else if idx := s.index(); idx == nil || idx.store == nil || idx.syncer == nil {
			status.State = "index"
		} else if full, err := idx.store.Meta(index.MetaFullAt); err != nil {
			status.State, status.Error = "error", err.Error()
		} else {
			progress := idx.syncer.Progress()
			status.Error = progress.Error
			if full == "" {
				status.State = "index"
			} else {
				q.Offset = c.Offsets[s.key]
				var rows []index.Row
				var total int
				var plays map[string]RecentPlay
				var err error
				if advanced == nil {
					rows, total, err = idx.store.Query(q)
				} else {
					rows, total, plays, err = d.aggregateSearch(s, idx.store, *advanced, q.Offset)
				}
				if err != nil {
					if sessionEnded(err) {
						status.State, status.Error = "signin", err.Error()
					} else if _, apiErr := s.api(); sessionEnded(apiErr) {
						status.State, status.Error = "signin", apiErr.Error()
					} else {
						status.State, status.Error = "error", err.Error()
					}
				} else {
					if q.Offset > total {
						return AggregatePage{}, errors.New("stale aggregate cursor")
					}
					status.State = "ready"
					sources = append(sources, aggregateSource{account: s.key, store: idx.store, rows: rows, total: total, plays: plays})
					page.Total += total
				}
			}
		}
		page.Accounts = append(page.Accounts, status)
	}
	// A cursor's offsets also record the accounts participating in the merge.
	if cursor != "" {
		for _, status := range page.Accounts {
			_, wasReady := c.Offsets[status.Account]
			if wasReady != (status.State == "ready") {
				return AggregatePage{}, errors.New("stale aggregate cursor: account availability changed")
			}
		}
	}
	for _, source := range sources {
		if _, ok := c.Offsets[source.account]; !ok {
			c.Offsets[source.account] = 0
		}
	}
	// Resolve only selected paths. If a store fails, discard that account and
	// repeat the merge using the already fetched candidates of the others.
	for {
		page.Entries = page.Entries[:0]
		for i := range sources {
			sources[i].used = 0
		}
		failed := -1
		paths := make(map[string]map[string]string)
		for len(page.Entries) < listPageSize {
			best := -1
			for i := range sources {
				s := &sources[i]
				if s.used >= len(s.rows) {
					continue
				}
				if best < 0 || aggregateLess(s.rows[s.used], s.account, s.plays, sources[best].rows[sources[best].used], sources[best].account, sources[best].plays, q.Sort, q.Asc) {
					best = i
				}
			}
			if best < 0 {
				break
			}
			s := &sources[best]
			row := s.rows[s.used]
			if paths[s.account] == nil {
				paths[s.account] = map[string]string{}
			}
			path, ok := paths[s.account][row.ParentID]
			if !ok {
				crumbs, err := s.store.Path(row.ParentID)
				if err != nil {
					for i := range page.Accounts {
						if page.Accounts[i].Account == s.account {
							page.Accounts[i].State, page.Accounts[i].Error = "error", err.Error()
							break
						}
					}
					failed = best
					break
				}
				path = folderPath(crumbs)
				paths[s.account][row.ParentID] = path
			}
			entry := toEntry(rowToFile(row))
			entry.Path = path
			page.Entries = append(page.Entries, AggregateEntry{Account: s.account, Entry: entry})
			s.used++
		}
		if failed < 0 {
			break
		}
		if cursor != "" {
			return AggregatePage{}, errors.New("stale aggregate cursor: account unavailable")
		}
		page.Total -= sources[failed].total
		delete(c.Offsets, sources[failed].account)
		sources = append(sources[:failed], sources[failed+1:]...)
	}
	more := false
	for _, s := range sources {
		c.Offsets[s.account] += s.used
		if c.Offsets[s.account] < s.total {
			more = true
		}
	}
	if more {
		data, err := json.Marshal(c)
		if err != nil {
			return AggregatePage{}, fmt.Errorf("aggregate cursor: %w", err)
		}
		page.Next = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func (d *DriveService) aggregateSearch(s *accountSession, store *index.Store, q AIQuery, offset int) ([]index.Row, int, map[string]RecentPlay, error) {
	search := q.search()
	search.Limit, search.Offset = listPageSize, offset
	if q.FolderID != "" {
		row, found, err := store.Get(q.FolderID)
		if err != nil {
			return nil, 0, nil, err
		}
		if !found || row.Trashed || row.Kind != drive.KindFolder {
			return nil, 0, nil, nil
		}
		search.Under = []string{q.FolderID}
	} else if q.Under != "" {
		matches, err := findFolders(store, q.Under)
		if err != nil {
			return nil, 0, nil, err
		}
		if len(matches) == 0 {
			return nil, 0, nil, nil
		}
		for _, m := range matches {
			if m.ID == "" {
				search.Under = nil
				break
			}
			search.Under = append(search.Under, m.ID)
		}
	}
	var plays map[string]RecentPlay
	if q.Played != "" {
		list, err := d.playsOf(s, drive.MaxListLimit, 1)
		if err != nil {
			return nil, 0, nil, err
		}
		if _, apiErr := s.api(); sessionEnded(apiErr) {
			return nil, 0, nil, apiErr
		}
		plays = make(map[string]RecentPlay, len(list))
		search.IDs = []string{}
		for _, p := range list {
			if q.Played == "unfinished" && !unfinished(p) || q.Played == "finished" && !finished(p.Position, p.Duration) {
				continue
			}
			plays[p.FileID] = p
			if q.IDs == nil {
				search.IDs = append(search.IDs, p.FileID)
			}
		}
		if q.IDs != nil {
			for _, id := range q.IDs {
				if _, ok := plays[id]; ok {
					search.IDs = append(search.IDs, id)
				}
			}
		}
	}
	if q.Sort == "played" {
		search.OrderIDs = make([]string, 0, len(plays))
		for id := range plays {
			search.OrderIDs = append(search.OrderIDs, id)
		}
		sort.Slice(search.OrderIDs, func(i, j int) bool {
			a, b := search.OrderIDs[i], search.OrderIDs[j]
			cmp := playedAt(plays[a].PlayedAt).Compare(playedAt(plays[b].PlayedAt))
			if cmp != 0 {
				if q.Asc {
					return cmp < 0
				}
				return cmp > 0
			}
			return a < b
		})
	}
	rows, total, err := store.Search(search)
	if err != nil {
		return nil, 0, nil, err
	}
	return rows, total, plays, nil
}

func aggregateLess(a index.Row, accountA string, playsA map[string]RecentPlay, b index.Row, accountB string, playsB map[string]RecentPlay, sortKey string, asc bool) bool {
	cmp := 0
	switch sortKey {
	case "name":
		cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	case "created":
		cmp = strings.Compare(a.CreatedTime, b.CreatedTime)
	case "played":
		cmp = playedAt(playsA[a.ID].PlayedAt).Compare(playedAt(playsB[b.ID].PlayedAt))
	case "size":
		if a.Size < b.Size {
			cmp = -1
		} else if a.Size > b.Size {
			cmp = 1
		}
	default:
		cmp = strings.Compare(a.ModifiedTime, b.ModifiedTime)
	}
	if cmp != 0 {
		if asc {
			return cmp < 0
		}
		return cmp > 0
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return accountA < accountB
}
