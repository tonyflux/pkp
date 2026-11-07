package lanshare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/tonyflux/pkp/internal/lanmedia"
)

// Each scope shares live metadata only within one bounded operation.
func (m *Manager) sourceProofContext(ctx context.Context) context.Context {
	if cloud, ok := m.cloud.(interface {
		SourceProofContext(context.Context) context.Context
	}); ok {
		return cloud.SourceProofContext(ctx)
	}
	return ctx
}

type visitorSpace struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	AllowPrepare bool   `json:"allowPrepare"`
}

func publicSpace(s spaceRecord) visitorSpace {
	return visitorSpace{ID: s.ID, Name: s.Name, Kind: s.Kind, AllowPrepare: s.AllowPrepare}
}

func (m *Manager) visitorSpaces(ctx context.Context) []visitorSpace {
	m.mu.Lock()
	spaces := make([]spaceRecord, 0, len(m.spaces))
	for _, s := range m.spaces {
		if s.Enabled {
			copy := *s
			copy.References = nil
			spaces = append(spaces, copy)
		}
	}
	m.mu.Unlock()
	sort.Slice(spaces, func(i, j int) bool { return spaces[i].Name < spaces[j].Name })
	result := make([]visitorSpace, 0, len(spaces))
	for _, s := range spaces {
		if ctx.Err() != nil {
			break
		}
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := m.validateSpace(check, s)
		cancel()
		if err == nil {
			if current, e := m.space(s.ID, false); e == nil {
				result = append(result, publicSpace(current))
			}
		}
	}
	return result
}

type visitorFiles struct {
	Revision string   `json:"revision"`
	NextPage string   `json:"nextPage,omitempty"`
	Entries  *[]Entry `json:"entries,omitempty"`
}

type visitorState struct {
	Spaces        *[]visitorSpace `json:"spaces,omitempty"`
	Space         *visitorSpace   `json:"space,omitempty"`
	Files         *visitorFiles   `json:"files,omitempty"`
	Tasks         *[]Task         `json:"tasks,omitempty"`
	Selected      *Entry          `json:"selected,omitempty"`
	SelectedError string          `json:"selectedError,omitempty"`
}

func (m *Manager) visitorSnapshot(w http.ResponseWriter, r *http.Request, response *activeResponse) {
	ctx := r.Context()
	q := r.URL.Query()
	sid := q.Get("space")
	snapshot := visitorState{}
	if sid != "" {
		if err := m.bindResponse(response, sid, "", ""); err != nil {
			jsonError(w, err)
			return
		}
		s, err := m.space(sid, false)
		if err == nil {
			err = m.validateSpace(ctx, s)
		}
		if err != nil {
			jsonError(w, err)
			return
		}
		current := publicSpace(s)
		snapshot.Space = &current
		if q.Get("files") == "1" {
			entries, next, err := m.visitorEntries(ctx, s, q.Get("parent"), q.Get("page"))
			if err != nil {
				jsonError(w, err)
				return
			}
			digest := sha256.New()
			if err := json.NewEncoder(digest).Encode(entries); err != nil {
				jsonError(w, err)
				return
			}
			digest.Write([]byte(next))
			revision := hex.EncodeToString(digest.Sum(nil))
			snapshot.Files = &visitorFiles{Revision: revision, NextPage: next}
			if q.Get("revision") != revision {
				snapshot.Files.Entries = &entries
			}
		}
		if tasks := q.Get("tasks"); tasks != "" {
			if tasks != "all" && tasks != "active" {
				jsonError(w, errInvalid)
				return
			}
			list, err := m.taskList(ctx, sid, response.Session, tasks == "active")
			if err != nil {
				jsonError(w, err)
				return
			}
			snapshot.Tasks = &list
		}
		if id := q.Get("id"); id != "" {
			source, err := m.resolve(ctx, sid, id)
			if err != nil {
				snapshot.SelectedError = errorCode(err)
			} else {
				m.decorate(&source.Entry, sid)
				snapshot.Selected = &source.Entry
			}
		}
	}
	if sid == "" || q.Get("spaces") == "1" {
		spaces := m.visitorSpaces(ctx)
		snapshot.Spaces = &spaces
	}
	if ctx.Err() != nil {
		return
	}
	jsonResponse(w, snapshot)
}

type playbackRequest struct {
	SpaceID      string                `json:"spaceId"`
	FileID       string                `json:"fileId"`
	Variant      string                `json:"variant"`
	Capabilities lanmedia.Capabilities `json:"capabilities"`
}

type playbackResult struct {
	Entry   Entry          `json:"entry"`
	Plan    *lanmedia.Plan `json:"plan,omitempty"`
	Variant string         `json:"variant,omitempty"`
}

func (m *Manager) visitorPlayback(w http.ResponseWriter, r *http.Request, response *activeResponse) {
	var request playbackRequest
	if err := readJSON(w, r, &request); err != nil {
		jsonError(w, err)
		return
	}
	if request.Variant != "" && request.Variant != "original" && request.Variant != "compatible" {
		jsonError(w, errInvalid)
		return
	}
	source, err := m.resolve(r.Context(), request.SpaceID, request.FileID)
	if err != nil {
		jsonError(w, err)
		return
	}
	if source.Entry.IsDir {
		jsonError(w, errInvalid)
		return
	}
	if err := m.bindResponse(response, request.SpaceID, request.FileID, source.Entry.Version); err != nil {
		jsonError(w, err)
		return
	}
	m.decorate(&source.Entry, request.SpaceID)
	result := playbackResult{Entry: source.Entry}
	if !source.Entry.Ready && !source.Entry.CompatibleReady {
		jsonResponse(w, result)
		return
	}
	if request.Variant != "original" && source.Entry.CompatibleReady {
		info, err := m.probe(r.Context(), request.SpaceID, source, "compatible")
		if err != nil {
			jsonError(w, err)
			return
		}
		plan, planErr := lanmedia.MakePlan(info, request.Capabilities)
		if plan.Kind == "direct" {
			result.Plan = &plan
			result.Variant = "compatible"
			jsonResponse(w, result)
			return
		}
		if !source.Entry.Ready {
			if planErr != nil && !errors.Is(planErr, lanmedia.ErrUnsupported) {
				jsonError(w, planErr)
				return
			}
			result.Plan = &plan
			result.Variant = "compatible"
			jsonResponse(w, result)
			return
		}
	}
	if !source.Entry.Ready {
		jsonError(w, errNotReady)
		return
	}
	info, err := m.probe(r.Context(), request.SpaceID, source, "original")
	if err != nil {
		jsonError(w, err)
		return
	}
	plan, err := lanmedia.MakePlan(info, request.Capabilities)
	if err != nil && plan.Kind != "unsupported" {
		jsonError(w, err)
		return
	}
	result.Plan = &plan
	result.Variant = "original"
	jsonResponse(w, result)
}
