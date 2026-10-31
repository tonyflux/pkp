// Package foldersync owns durable folder relationships independently of transfer history.
package foldersync

import (
	"context"
	"encoding/json"

	"github.com/tonyflux/pkp/internal/i18n"
)

const (
	Bidirectional = "bidirectional"
	Backup        = "backup"
	Download      = "download"
)

var (
	ErrAuth       = i18n.New("同步账号需要重新登录")
	ErrOffline    = i18n.New("同步正在等待网络")
	ErrPermission = i18n.New("同步没有访问权限")
	ErrNotFound   = i18n.New("同步项目不存在")
	ErrUnknown    = i18n.New("同步操作结果待确认，已停止自动重试")
	ErrRepair     = i18n.New("同步基线或根身份无法验证，请重新扫描并配对")
	ErrChanged    = i18n.New("同步内容已经变化，请重新审阅预览")
)

type Config struct {
	ID        string          `json:"id"`
	Account   string          `json:"account"`
	Name      string          `json:"name"`
	LocalPath string          `json:"localPath"`
	CloudID   string          `json:"cloudId"`
	CloudPath string          `json:"cloudPath"`
	Mode      string          `json:"mode"`
	Deletes   map[string]bool `json:"deletes"`
}

type Node struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	ParentID  string   `json:"parentId"`
	Hash      string   `json:"hash"`
	Modified  string   `json:"modified"`
	Version   string   `json:"version"`
	Phase     string   `json:"phase"`
	Directory bool     `json:"directory"`
	Size      int64    `json:"size"`
	Ancestors []string `json:"ancestors,omitempty"`
}

type Issue struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason"`
	KeepName string `json:"keepName"`
	Local    *Node  `json:"local"`
	Cloud    *Node  `json:"cloud"`
}

type Activity struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	Path      string `json:"path"`
	Direction string `json:"direction"`
	Action    string `json:"action"`
	Result    string `json:"result"`
	Error     string `json:"error"`
}

type Progress struct {
	Path      string `json:"path"`
	Direction string `json:"direction"`
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Completed int    `json:"completed"`
	Files     int    `json:"files"`
}

type Pair struct {
	Config
	State       string     `json:"state"`
	Phase       string     `json:"phase"`
	Error       string     `json:"error"`
	Paused      bool       `json:"paused"`
	LastAttempt string     `json:"lastAttempt"`
	LastSuccess string     `json:"lastSuccess"`
	Issues      []Issue    `json:"issues"`
	Activities  []Activity `json:"activities"`
	Progress    Progress   `json:"progress"`
	DeletePlan  *Preview   `json:"deletePlan"`
}

type Change struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Action    string `json:"action"`
	Direction string `json:"direction"`
	Reason    string `json:"reason"`
	KeepName  string `json:"keepName"`
	Size      int64  `json:"size"`
	Directory bool   `json:"directory"`
}

type Preview struct {
	ID                string   `json:"id"`
	Config            Config   `json:"config"`
	Changes           []Change `json:"changes"`
	Uploads           int      `json:"uploads"`
	Downloads         int      `json:"downloads"`
	Conflicts         int      `json:"conflicts"`
	Skipped           int      `json:"skipped"`
	Deletes           int      `json:"deletes"`
	Directories       int      `json:"directories"`
	DeleteDirectories int      `json:"deleteDirectories"`
	Tracked           int      `json:"tracked"`
	Bytes             int64    `json:"bytes"`
	Blocked           bool     `json:"blocked"`
	Error             string   `json:"error"`
}

type UploadRequest struct {
	ID, Path, ParentID, Name, Checkpoint string
	FileID                               string
	Grant                                json.RawMessage
	SaveSlot                             func(fileID string, grant json.RawMessage) error
}

type Backend interface {
	AccountID() string
	Root(context.Context, string) (Node, error)
	List(context.Context, string) ([]Node, error)
	Hash(context.Context, Node) (string, error)
	Mkdir(context.Context, string, string, string) (Node, error)
	Upload(context.Context, UploadRequest, func(int64, int64)) (Node, error)
	Download(context.Context, Node, string, string, string, func(int64, int64)) error
	Rename(context.Context, Node, string, string) (Node, error)
	Move(context.Context, Node, string, string, string) (Node, error)
	Trash(context.Context, Node, string) error
	Trashed(context.Context, string) (bool, error)
	Get(context.Context, string) (Node, error)
}

type Options struct {
	Dir      string
	Backend  func(account string) (Backend, error)
	OnChange func([]Pair)
}
