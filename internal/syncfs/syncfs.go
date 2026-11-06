// Package syncfs is the filesystem trust boundary for folder sync.
package syncfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tonyflux/pkp/internal/i18n"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type Identity struct {
	Path             string `json:"path"`
	Volume           string `json:"volume"`
	File             string `json:"file"`
	CaseSensitive    bool   `json:"caseSensitive"`
	UnicodeSensitive bool   `json:"unicodeSensitive"`
}

type Entry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	ModTime   string `json:"modTime"`
	FileID    string `json:"fileId"`
	Hash      string `json:"hash"`
	// Version includes change time: same-size writes with restored mtime invalidate hashes.
	Version string `json:"version,omitempty"`
}

type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

var (
	ErrRootChanged   = i18n.New("同步根目录身份已改变，请重新配对")
	ErrUnavailable   = i18n.New("同步磁盘或目录不可用")
	ErrPermission    = i18n.New("没有同步目录的访问权限")
	ErrUnsupported   = i18n.New("此文件系统不支持安全同步操作")
	ErrUnsafePath    = i18n.New("同步路径不安全或包含符号链接")
	ErrUnstable      = i18n.New("文件正在变化，请稍后重新扫描")
	ErrStageOccupied = i18n.New(".pkp-sync 已被用户文件占用，不能用于同步暂存")
	ErrCollision     = i18n.New("文件名与现有名称冲突")
)

func pathError(path string, err error) error {
	if err == nil {
		return nil
	}
	category := ErrUnavailable
	if errors.Is(err, fs.ErrPermission) {
		category = ErrPermission
	}
	return fmt.Errorf("%w: %s (%w)", category, path, err)
}

// Identify permits canonical ancestor aliases (such as /var on macOS), never a link root.
func Identify(path string) (Identity, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Identity{}, pathError(path, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return Identity{}, pathError(absolute, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || isReparse(info) {
		return Identity{}, ErrUnsafePath
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Identity{}, pathError(absolute, err)
	}
	id, err := platformIdentity(canonical)
	if err != nil {
		return Identity{}, err
	}
	if err = checkComponents(id.Path); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func Validate(id Identity) error {
	if id.Path == "" || id.Volume == "" || id.File == "" {
		return ErrRootChanged
	}
	if err := checkComponents(id.Path); err != nil {
		return err
	}
	fresh, err := platformIdentity(id.Path)
	if err != nil {
		return err
	}
	if !sameVolumeFile(fresh.Volume, fresh.File, id.Volume, id.File) || fresh.Path != id.Path || fresh.CaseSensitive != id.CaseSensitive || fresh.UnicodeSensitive != id.UnicodeSensitive {
		return ErrRootChanged
	}
	dir, err := os.Open(id.Path)
	if err != nil {
		return pathError(id.Path, err)
	}
	defer dir.Close()
	// Opening alone does not prove directory enumeration permission.
	_, err = dir.Readdirnames(1)
	if err != nil && err != io.EOF {
		return pathError(id.Path, err)
	}
	return nil
}

func checkComponents(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return pathError(current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparse(info) {
			return ErrUnsafePath
		}
		if !info.IsDir() {
			return ErrUnsafePath
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func sameVolumeFile(aVolume, aFile, bVolume, bFile string) bool {
	if aVolume == "" || aVolume != bVolume || aFile == "" || bFile == "" {
		return false
	}
	if aFile == bFile {
		return true
	}
	if runtime.GOOS != "darwin" {
		return false
	}
	// Darwin st_dev can change after remount/reboot; the volume UUID and inode cannot.
	inode := func(file string) (uint64, bool) {
		device, number, ok := strings.Cut(file, ":")
		dev, deviceErr := strconv.ParseUint(device, 10, 64)
		ino, inodeErr := strconv.ParseUint(number, 10, 64)
		return ino, ok && deviceErr == nil && inodeErr == nil && dev != 0 && ino != 0
	}
	a, aOK := inode(aFile)
	b, bOK := inode(bFile)
	return aOK && bOK && a == b
}

// Overlaps is deliberately conservative for Unicode canonical aliases, even on
// volumes that allow both spellings. A second writer must never claim that alias.
func Overlaps(a, b Identity) bool {
	if sameVolumeFile(a.Volume, a.File, b.Volume, b.File) {
		return true
	}
	sensitive := a.CaseSensitive && b.CaseSensitive
	key := func(path string) []string {
		path = norm.NFC.String(filepath.ToSlash(filepath.Clean(path)))
		if !sensitive {
			path = cases.Fold().String(path)
		}
		return strings.Split(strings.TrimRight(path, "/"), "/")
	}
	aa, bb := key(a.Path), key(b.Path)
	n := min(len(aa), len(bb))
	for i := range n {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func CollisionKey(name string, id Identity) string {
	key := norm.NFC.String(name)
	if !id.CaseSensitive {
		key = cases.Fold().String(key)
	}
	return key
}

func NameError(name, goos string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || strings.ContainsAny(name, "/\x00") {
		return ErrUnsafePath
	}
	if goos != "windows" && len(name) > 255 {
		return i18n.Errorf("文件名超出平台长度限制：%s", name)
	}
	if goos != "windows" {
		return nil
	}
	if len(utf16.Encode([]rune(name))) > 255 || strings.ContainsAny(name, `<>:"\|?*`) || strings.TrimRight(name, ". ") != name {
		return i18n.Errorf("Windows 无法表示此文件名：%s", name)
	}
	for _, r := range name {
		if r < 32 {
			return i18n.Errorf("Windows 无法表示此文件名：%s", name)
		}
	}
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	base = strings.TrimRight(base, " ")
	reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" || base == "CONIN$" || base == "CONOUT$"
	if len([]rune(base)) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		reserved = reserved || strings.ContainsRune("123456789¹²³", []rune(base)[3])
	}
	if reserved {
		return i18n.Errorf("Windows 无法表示此文件名：%s", name)
	}
	return nil
}

// ValidateRelative is required immediately before each write; it never resolves
// symlinks, accepts no cleaned traversal, and validates still-missing components.
func ValidateRelative(root Identity, rel string) (string, error) {
	if err := Validate(root); err != nil {
		return "", err
	}
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") || strings.ContainsRune(rel, 0) {
		return "", ErrUnsafePath
	}
	parts := strings.Split(rel, "/")
	absolute := root.Path
	missing := false
	for _, part := range parts {
		if err := NameError(part, runtime.GOOS); err != nil {
			return "", err
		}
		absolute = filepath.Join(absolute, part)
		if missing {
			continue
		}
		info, err := os.Lstat(absolute)
		if errors.Is(err, fs.ErrNotExist) {
			missing = true
			continue
		}
		if err != nil {
			return "", pathError(absolute, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparse(info) {
			return "", ErrUnsafePath
		}
		if absolute != filepath.Join(root.Path, filepath.FromSlash(rel)) && !info.IsDir() {
			return "", ErrUnsafePath
		}
		volume, _, err := platformFile(info, absolute)
		if err != nil {
			return "", err
		}
		if volume != root.Volume {
			return "", ErrRootChanged
		}
		if runtime.GOOS == "windows" && info.IsDir() {
			directoryID, err := platformIdentity(absolute)
			if err != nil {
				return "", err
			}
			if directoryID.CaseSensitive != root.CaseSensitive {
				return "", ErrUnsupported
			}
		}
	}
	// Keep paths usable by the shell recycle APIs, not just long-path-aware Go calls.
	if runtime.GOOS == "windows" && len(utf16.Encode([]rune(absolute))) >= 260 {
		return "", i18n.Errorf("同步路径超出平台长度限制：%s", absolute)
	}
	return absolute, nil
}

const stageName = ".pkp-sync"
const markerName = "owner.json"

// IsStagePath reports an exact reserved staging component anywhere in an absolute
// or relative path, using the root's filesystem name semantics. It is lexical;
// callers must also validate the identity and check canonical opened paths.
func IsStagePath(path string, identity Identity) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if CollisionKey(part, identity) == stageName {
			return true
		}
	}
	return false
}

type stageOwner struct {
	Version int    `json:"version"`
	Pair    string `json:"pair"`
	Volume  string `json:"volume"`
	Root    string `json:"root"`
}

func readStage(root Identity, pair string) error {
	path := filepath.Join(root.Path, stageName)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparse(info) {
		return ErrStageOccupied
	}
	f, err := openNoFollow(filepath.Join(path, markerName))
	if err != nil {
		return ErrStageOccupied
	}
	defer f.Close()
	var owner stageOwner
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	if dec.Decode(&owner) != nil || owner.Version != 1 || owner.Pair == "" || !sameVolumeFile(owner.Volume, owner.Root, root.Volume, root.File) || (pair != "" && owner.Pair != pair) {
		return ErrStageOccupied
	}
	if dec.Decode(new(any)) != io.EOF {
		return ErrStageOccupied
	}
	return nil
}

// EnsureStage never adopts or cleans an arbitrary directory of the same name.
func EnsureStage(root Identity, pairID string) (string, error) {
	if pairID == "" {
		return "", ErrStageOccupied
	}
	path, err := ValidateRelative(root, stageName)
	if err != nil {
		return "", err
	}
	if err = readStage(root, pairID); err == nil {
		return path, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	handle, err := os.OpenRoot(root.Path)
	if err != nil {
		return "", pathError(root.Path, err)
	}
	defer handle.Close()
	if err = Validate(root); err != nil {
		return "", err
	}
	if err = handle.Mkdir(stageName, 0700); err != nil {
		return "", pathError(path, err)
	}
	staged, err := handle.OpenRoot(stageName)
	if err != nil {
		return "", pathError(path, err)
	}
	defer staged.Close()
	owner := stageOwner{Version: 1, Pair: pairID, Volume: root.Volume, Root: root.File}
	data, err := json.Marshal(owner)
	if err != nil {
		return "", err
	}
	f, err := staged.OpenFile(markerName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", pathError(path, err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", pathError(path, err)
	}
	if err = Validate(root); err != nil {
		return "", err
	}
	return path, nil
}

func excluded(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case ".ds_store", "thumbs.db", "desktop.ini", ".spotlight-v100", ".trashes", ".fseventsd", "$recycle.bin", "system volume information":
		return true
	}
	return strings.HasPrefix(name, "~$") || strings.HasSuffix(lower, ".crdownload") || strings.HasSuffix(lower, ".download") || strings.HasSuffix(lower, ".part") || strings.HasSuffix(lower, ".partial") || strings.HasSuffix(lower, ".swp") || strings.HasSuffix(lower, ".swo") || strings.HasSuffix(name, "~")
}

type stamp struct {
	size     int64
	modified time.Time
	version  string
	file     string
	mode     fs.FileMode
}

func fileStamp(info fs.FileInfo, path string) (stamp, error) {
	_, id, err := platformFile(info, path)
	if err != nil {
		return stamp{}, err
	}
	version := platformVersion(info, path)
	if version == "" {
		return stamp{}, ErrUnstable
	}
	return stamp{info.Size(), info.ModTime(), version, id, info.Mode()}, nil
}
func sameStamp(a, b stamp) bool {
	return a.size == b.size && a.modified.Equal(b.modified) && a.version == b.version && a.file == b.file && a.mode == b.mode
}

func hashFile(ctx context.Context, f *os.File) (string, error) {
	digest := sha256.New()
	buf := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = digest.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func Hash(ctx context.Context, path string) (string, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return "", pathError(path, err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return "", pathError(path, err)
	}
	if !before.Mode().IsRegular() {
		return "", ErrUnsafePath
	}
	a, err := fileStamp(before, path)
	if err != nil {
		return "", err
	}
	sum, err := hashFile(ctx, f)
	if err != nil {
		return "", pathError(path, err)
	}
	after, err := f.Stat()
	if err != nil {
		return "", pathError(path, err)
	}
	b, err := fileStamp(after, path)
	if err != nil {
		return "", err
	}
	fresh, err := os.Lstat(path)
	if err != nil {
		return "", ErrUnstable
	}
	if !os.SameFile(before, fresh) {
		return "", ErrUnstable
	}
	c, err := fileStamp(fresh, path)
	if err != nil {
		return "", err
	}
	if !sameStamp(a, b) || !sameStamp(a, c) {
		return "", ErrUnstable
	}
	return sum, nil
}

// FileIdentity identifies a regular file without reading its content.
func FileIdentity(path string) (string, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return "", pathError(path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", pathError(path, err)
	}
	if !info.Mode().IsRegular() {
		return "", ErrUnsafePath
	}
	volume, id, err := platformFile(info, path)
	if err != nil {
		return "", err
	}
	fresh, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, fresh) {
		return "", ErrUnstable
	}
	return volume + ":" + id, nil
}

// Inspect distinguishes an absent candidate from an unavailable or replaced root.
func Inspect(ctx context.Context, root Identity, rel string) (*Entry, error) {
	path, err := ValidateRelative(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err = Validate(root); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, pathError(path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || isReparse(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
		return nil, ErrUnsafePath
	}
	before, err := fileStamp(info, path)
	if err != nil {
		return nil, err
	}
	entry := &Entry{Path: rel, Name: info.Name(), Directory: info.IsDir(), Size: info.Size(), ModTime: info.ModTime().UTC().Format(time.RFC3339Nano), FileID: before.file, Version: before.version}
	if entry.Directory {
		entry.Size = 0
	} else {
		entry.Hash, err = Hash(ctx, path)
		if err != nil {
			return nil, err
		}
	}
	latest, err := os.Lstat(path)
	if err != nil {
		return nil, ErrUnstable
	}
	after, err := fileStamp(latest, path)
	if err != nil {
		return nil, err
	}
	if !sameStamp(before, after) {
		return nil, ErrUnstable
	}
	if err = Validate(root); err != nil {
		return nil, err
	}
	return entry, nil
}

// Scan publishes no partial snapshot. Directory versions and every observed file
// are closed over again after hashing, so concurrent removals never look like deletes.
func Scan(ctx context.Context, root Identity, cache map[string]Entry) ([]Entry, []Skipped, error) {
	if err := Validate(root); err != nil {
		return nil, nil, err
	}
	var entries []Entry
	var skipped []Skipped
	observed := make(map[string]stamp)
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		absolute := root.Path
		if rel != "" {
			absolute = filepath.Join(root.Path, filepath.FromSlash(rel))
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return pathError(absolute, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparse(info) {
			return ErrUnstable
		}
		volume, _, err := platformFile(info, absolute)
		if err != nil {
			return err
		}
		if volume != root.Volume {
			return ErrRootChanged
		}
		if runtime.GOOS == "windows" {
			directoryID, err := platformIdentity(absolute)
			if err != nil {
				return err
			}
			if directoryID.CaseSensitive != root.CaseSensitive {
				return ErrUnsupported
			}
		}
		st, err := fileStamp(info, absolute)
		if err != nil {
			return err
		}
		observed[rel] = st
		dir, err := openNoFollow(absolute)
		if err != nil {
			return pathError(absolute, err)
		}
		children, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			return pathError(absolute, err)
		}
		keys := make(map[string]string)
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := child.Name()
			sub := name
			if rel != "" {
				sub = rel + "/" + name
			}
			path := filepath.Join(absolute, name)
			childInfo, err := os.Lstat(path)
			if err != nil {
				return ErrUnstable
			}
			if CollisionKey(name, root) == CollisionKey(stageName, root) && rel == "" {
				if name != stageName {
					return ErrStageOccupied
				}
				if err := readStage(root, ""); err != nil {
					return ErrStageOccupied
				}
				continue
			}
			if childInfo.Mode()&os.ModeSymlink != 0 || isReparse(childInfo) {
				skipped = append(skipped, Skipped{sub, i18n.T("符号链接不会参与同步")})
				continue
			}
			if excluded(name) {
				skipped = append(skipped, Skipped{sub, i18n.T("已识别的临时或系统控制文件")})
				continue
			}
			if err := NameError(name, runtime.GOOS); err != nil {
				skipped = append(skipped, Skipped{sub, err.Error()})
				continue
			}
			key := CollisionKey(name, root)
			if other, ok := keys[key]; ok && other != sub {
				skipped = append(skipped, Skipped{other, ErrCollision.Error()}, Skipped{sub, ErrCollision.Error()})
			} else {
				keys[key] = sub
			}
			if !childInfo.IsDir() && !childInfo.Mode().IsRegular() {
				skipped = append(skipped, Skipped{sub, i18n.T("不支持同步此特殊文件")})
				continue
			}
			volume, _, err := platformFile(childInfo, path)
			if err != nil {
				return err
			}
			if volume != root.Volume {
				return ErrRootChanged
			}
			st, err := fileStamp(childInfo, path)
			if err != nil {
				return err
			}
			entry := Entry{Path: sub, Name: name, Directory: childInfo.IsDir(), Size: childInfo.Size(), ModTime: childInfo.ModTime().UTC().Format(time.RFC3339Nano), FileID: st.file, Version: st.version}
			if childInfo.IsDir() {
				entry.Size = 0
				if err := walk(sub); err != nil {
					return err
				}
			} else {
				previous, ok := cache[sub]
				if ok && !previous.Directory && previous.Size == entry.Size && previous.ModTime == entry.ModTime && previous.FileID == entry.FileID && previous.Version == entry.Version && entry.Version != "" && len(previous.Hash) == 64 {
					entry.Hash = previous.Hash
				} else {
					entry.Hash, err = Hash(ctx, path)
					if err != nil {
						return err
					}
				}
				observed[sub] = st
			}
			entries = append(entries, entry)
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, nil, err
	}
	for rel, before := range observed {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		absolute := filepath.Join(root.Path, filepath.FromSlash(rel))
		info, err := os.Lstat(absolute)
		if err != nil {
			return nil, nil, ErrUnstable
		}
		after, err := fileStamp(info, absolute)
		if err != nil {
			return nil, nil, err
		}
		if !sameStamp(before, after) {
			return nil, nil, ErrUnstable
		}
	}
	if err := Validate(root); err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, skipped, nil
}

func debounce(ctx context.Context, events <-chan struct{}, notify func(), closeNative func()) func() {
	child, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); closeNative() }) }
	go func() {
		defer stop()
		var timer *time.Timer
		var tick <-chan time.Time
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case <-child.Done():
				return
			case _, ok := <-events:
				if !ok {
					return
				}
				if timer == nil {
					timer = time.NewTimer(5 * time.Second)
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(5 * time.Second)
				}
				tick = timer.C
			case <-tick:
				tick = nil
				notify()
			}
		}
	}()
	return stop
}
