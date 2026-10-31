package drive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// uploadTypeResumable asks the create endpoint for a cloud-storage upload
// slot: the server answers with short-lived OSS credentials and the client
// pushes the bytes itself.
const uploadTypeResumable = "UPLOAD_TYPE_RESUMABLE"

// providerAliyun is the only resumable provider the server hands out for
// ordinary files; the vault provider is for encrypted uploads.
const providerAliyun = "PROVIDER_ALIYUN"

// UploadParams describes a local file to create a slot for.
type UploadParams struct {
	// ParentID is the destination folder; empty means the root.
	ParentID string
	Name     string
	Size     int64
}

// OSSCredentials is the STS grant the server issues for one upload: where
// the bytes go, and the identity allowed to put them there until Expiration.
type OSSCredentials struct {
	AccessKeyID     string    `json:"access_key_id"`
	AccessKeySecret string    `json:"access_key_secret"`
	SecurityToken   string    `json:"security_token"`
	Endpoint        string    `json:"endpoint"`
	Bucket          string    `json:"bucket"`
	Key             string    `json:"key"`
	Expiration      time.Time `json:"expiration"`
	// CName is "true" when Endpoint is a custom domain that already names
	// the bucket, so the bucket must not be prefixed to the host.
	CName string `json:"cname"`
}

// UploadSlot is the placeholder file and OSS grant returned for an upload.
type UploadSlot struct {
	File File
	OSS  *OSSCredentials
}

// CreateUpload makes a placeholder file and requests an OSS upload grant.
func (c *Client) CreateUpload(ctx context.Context, params UploadParams) (*UploadSlot, error) {
	if params.Name == "" {
		return nil, errors.New("drive: upload name is required")
	}
	if params.Size < 0 {
		return nil, errors.New("drive: upload size is negative")
	}
	// The server rejects an empty hash. Use an independent 160-bit token
	// per create, not a content hash: no file pre-read or dedup lookup.
	var placeholder [20]byte
	if _, err := rand.Read(placeholder[:]); err != nil {
		return nil, err
	}
	body := map[string]any{
		"kind":        KindFile,
		"name":        params.Name,
		"size":        strconv.FormatInt(params.Size, 10),
		"hash":        strings.ToUpper(hex.EncodeToString(placeholder[:])),
		"upload_type": uploadTypeResumable,
		"resumable":   map[string]any{"provider": providerAliyun},
		"folder_type": "NORMAL",
	}
	if params.ParentID != "" {
		body["parent_id"] = params.ParentID
	}

	var out struct {
		UploadType string `json:"upload_type"`
		File       File   `json:"file"`
		Resumable  *struct {
			Provider string         `json:"provider"`
			Params   OSSCredentials `json:"params"`
		} `json:"resumable"`
	}
	if err := c.do(ctx, request{method: http.MethodPost, path: "/drive/v1/files", body: body}, &out); err != nil {
		return nil, err
	}
	if out.UploadType != uploadTypeResumable || out.Resumable == nil {
		return nil, errors.New("drive: server returned no resumable upload grant")
	}
	if out.Resumable.Provider != providerAliyun {
		return nil, errors.New("drive: upload provider " + out.Resumable.Provider + " is not supported")
	}
	if out.Resumable.Params.Key == "" || out.Resumable.Params.Bucket == "" || out.Resumable.Params.Endpoint == "" {
		return nil, errors.New("drive: upload grant is incomplete")
	}
	return &UploadSlot{File: out.File, OSS: &out.Resumable.Params}, nil
}
