package services

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/tonyflux/pkp/internal/i18n"
	"github.com/tonyflux/pkp/internal/tmdb"
)

// The AI assistant's conversations, kept on this computer per account in
// assistant-<key>.json (recent chats saved locally by account) and removed with the account.
// Bodies are stored and returned as plain JSON: ordinary content is bounded,
// while upload receipts remain complete even for large selected folders.

const (
	conversationKeep    = 20
	conversationVersion = 1
	maxConversationBody = 2 << 20
	maxConversationID   = 128
	maxConversationName = 200
)

var (
	errNoConversation       = i18n.New("找不到这段对话，它可能已被删除")
	errConversationTooLarge = &lazyError{func() string { return i18n.F("这段对话超过 %d MB，无法保存", maxConversationBody>>20) }}
	errConversationBody     = i18n.New("对话内容无效，无法保存")
	errConversationID       = i18n.New("对话标识无效")
)

// ConversationInfo is what the conversation list shows of one.
type ConversationInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"` // RFC 3339
	Turns     int    `json:"turns"`
	// PlanPending says a plan card is waiting for confirmation.
	PlanPending bool `json:"planPending"`
}

// savedConversation is one conversation as the file keeps it; the body is
// a string so that it comes back byte for byte.
type savedConversation struct {
	ConversationInfo
	Body string `json:"body"`
}

type conversationsFile struct {
	Version       int                 `json:"version"`
	Conversations []savedConversation `json:"conversations"`
}

func (d *DriveService) conversationsPath(key string) string {
	return d.accountFile(key, "assistant-", ".json")
}

// Conversations lists account's saved conversations, newest first.
func (d *DriveService) Conversations(account string) ([]ConversationInfo, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return nil, err
	}
	d.convMu.Lock()
	saved, err := d.readConversations(s.key)
	d.convMu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]ConversationInfo, len(saved))
	for i, c := range saved {
		out[i] = c.ConversationInfo
	}
	return out, nil
}

// Conversation is the body of one of account's conversations as saved.
func (d *DriveService) Conversation(account, id string) (string, error) {
	s, err := d.sessionFor(account)
	if err != nil {
		return "", err
	}
	d.convMu.Lock()
	saved, err := d.readConversations(s.key)
	d.convMu.Unlock()
	if err != nil {
		return "", err
	}
	if i := slices.IndexFunc(saved, func(c savedConversation) bool { return c.ID == id }); i >= 0 {
		return saved[i].Body, nil
	}
	return "", errNoConversation
}

// SaveConversation keeps one of account's conversations, in place of the
// one with its id, and drops the oldest beyond conversationKeep. Ordinary
// JSON content is limited to 2 MiB, excluding complete upload receipts.
// An UpdatedAt that is not RFC 3339 becomes now.
func (d *DriveService) SaveConversation(account string, info ConversationInfo, body string) error {
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	if info.ID == "" || len(info.ID) > maxConversationID {
		return errConversationID
	}
	if len(body) > maxConversationBody {
		if !conversationUploadReceiptsFit(body) {
			return errConversationTooLarge
		}
	} else if !json.Valid([]byte(body)) {
		return errConversationBody
	}
	info.Title = tmdb.CleanLine(info.Title, maxConversationName)
	info.Turns = max(info.Turns, 0)
	if at, err := time.Parse(time.RFC3339Nano, info.UpdatedAt); err == nil {
		info.UpdatedAt = at.UTC().Format(time.RFC3339)
	} else {
		info.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	d.convMu.Lock()
	defer d.convMu.Unlock()
	saved, err := d.readConversations(s.key)
	if err != nil {
		return err
	}
	saved = slices.DeleteFunc(saved, func(c savedConversation) bool { return c.ID == info.ID })
	// Newest first by the time each says (UTC RFC 3339, so the text
	// compares as the time); of two alike the one just saved leads.
	saved = append([]savedConversation{{ConversationInfo: info, Body: body}}, saved...)
	slices.SortStableFunc(saved, func(a, b savedConversation) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	if len(saved) > conversationKeep {
		saved = saved[:conversationKeep]
	}
	return d.writeConversations(s.key, saved)
}

// Only typed upload outcomes may grow past the ordinary conversation limit.
// Keep the original JSON intact so reopening never loses queued or failed inputs.
// The shape is exact — unknown fields fail the decode — because that is what
// keeps the exemption from carrying arbitrary payloads: a frontend change to
// these results has to change AssistantOperationItem with it.
func conversationUploadReceiptsFit(body string) bool {
	var view struct {
		Version int `json:"version"`
		Turns   []struct {
			Cards []struct {
				Kind    string          `json:"kind"`
				Action  string          `json:"action"`
				Results json.RawMessage `json:"results"`
			} `json:"cards"`
		} `json:"turns"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil || view.Version != conversationVersion {
		return false
	}
	size := len(body)
	for _, turn := range view.Turns {
		for _, card := range turn.Cards {
			if card.Kind != "operation" || (card.Action != "upload_files" && card.Action != "upload_folder") {
				continue
			}
			var results []AssistantOperationItem
			decoder := json.NewDecoder(bytes.NewReader(card.Results))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&results); err == nil && len(results) != 0 {
				size -= len(card.Results) - 2 // Leave the empty array in the ordinary budget.
			}
		}
	}
	return size <= maxConversationBody
}

// DeleteConversation forgets one of account's conversations; one already
// gone is not an error.
func (d *DriveService) DeleteConversation(account, id string) error {
	s, err := d.sessionFor(account)
	if err != nil {
		return err
	}
	d.convMu.Lock()
	defer d.convMu.Unlock()
	saved, err := d.readConversations(s.key)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(saved, func(c savedConversation) bool { return c.ID == id })
	if len(kept) == len(saved) {
		return nil
	}
	return d.writeConversations(s.key, kept)
}

// readConversations reads an account's file; convMu must be held. A
// missing file is no conversations, an unreadable one is moved aside.
func (d *DriveService) readConversations(key string) ([]savedConversation, error) {
	if d.configErr != nil {
		return nil, d.configErr
	}
	path := d.conversationsPath(key)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []savedConversation{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file conversationsFile
	if err := json.Unmarshal(data, &file); err != nil {
		d.log.Warn("assistant: conversations unreadable, starting empty", "path", path, "error", err)
		if err := os.Rename(path, path+".bad"); err != nil {
			d.log.Warn("assistant: could not move the unreadable conversations aside", "error", err)
		}
		return []savedConversation{}, nil
	}
	if file.Conversations == nil {
		file.Conversations = []savedConversation{}
	}
	return file.Conversations, nil
}

// writeConversations replaces an account's file atomically; convMu must be
// held.
func (d *DriveService) writeConversations(key string, saved []savedConversation) error {
	data, err := json.Marshal(conversationsFile{Version: conversationVersion, Conversations: saved})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.configDir, 0o700); err != nil {
		return err
	}
	path := d.conversationsPath(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
