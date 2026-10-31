package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The checks of the webview's scripts/i18n-check.mjs, for Go. Every non-test
// .go file of the module is read; I18N_ONLY=path,path (relative to the
// module root, files or directories) narrows the text check and the key
// check to those, and skips the unused-entry check.

var keyArg = map[string]int{"T": 0, "F": 0, "Errorf": 0, "New": 0, "N": 1}

type use struct{ key, where string }

func moduleRoot(t *testing.T) string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return true
		}
	}
	return false
}

// scanGo reports CJK string literals outside i18n calls and collects keys.
func scanGo(t *testing.T) (problems []string, uses []use, narrowed bool) {
	root := moduleRoot(t)
	var only []string
	for _, p := range strings.Split(os.Getenv("I18N_ONLY"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			only = append(only, filepath.Join(root, p))
		}
	}
	skipDirs := []string{"frontend", "build", "bin", "cloudflare", "node_modules", ".git", ".task", ".idea"}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (slices.Contains(skipDirs, d.Name()) || path == filepath.Join(root, "internal", "i18n")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if len(only) > 0 && !slices.ContainsFunc(only, func(o string) bool { return path == o || strings.HasPrefix(path, o+string(filepath.Separator)) }) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		lines := strings.Split(string(src), "\n")
		checkText := !strings.Contains(string(src), "i18n-ignore-file")
		ignored := func(line int) bool {
			return strings.Contains(lines[line-1], "i18n-ignore") || (line > 1 && strings.Contains(lines[line-2], "i18n-ignore"))
		}
		allowed := map[*ast.BasicLit]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				pkg, ok := sel.X.(*ast.Ident)
				i, known := keyArg[sel.Sel.Name]
				if !ok || pkg.Name != "i18n" || !known || len(n.Args) <= i {
					break
				}
				line := fset.Position(n.Args[i].Pos()).Line
				lit, ok := n.Args[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					problems = append(problems, rel+":"+strconv.Itoa(line)+": i18n."+sel.Sel.Name+" key must be a string literal")
					break
				}
				key, _ := strconv.Unquote(lit.Value)
				uses = append(uses, use{key, rel + ":" + strconv.Itoa(line)})
				allowed[lit] = true
			case *ast.Field:
				if n.Tag != nil {
					allowed[n.Tag] = true
				}
			case *ast.KeyValueExpr:
				if lit, ok := n.Key.(*ast.BasicLit); ok {
					allowed[lit] = true
				}
			case *ast.ImportSpec:
				allowed[n.Path] = true
			case *ast.BasicLit:
				if n.Kind != token.STRING || allowed[n] || !checkText {
					break
				}
				s, err := strconv.Unquote(n.Value)
				if err != nil || !hasCJK(s) {
					break
				}
				line := fset.Position(n.Pos()).Line
				if !ignored(line) {
					if len(s) > 80 {
						s = s[:80]
					}
					problems = append(problems, rel+":"+strconv.Itoa(line)+": "+strconv.Quote(s))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return problems, uses, len(only) > 0
}

var verb = regexp.MustCompile(`%%|%(?:\[\d+\])?[-+# 0]*(?:\d+|\*)?(?:\.(?:\d+|\*)?)?(?:\[\d+\])?[a-zA-Z]`)

func verbs(s string) int {
	n := 0
	for _, m := range verb.FindAllString(s, -1) {
		if m != "%%" {
			n++
		}
	}
	return n
}

// TestNoUntranslatedText fails on Chinese, Japanese or Korean in a string
// literal that is not an i18n key: user-facing text must go through T/F/N/
// Errorf/New. Struct tags, map keys and imports are data. A line whose text
// or previous line says i18n-ignore is skipped, and a file that says
// i18n-ignore-file anywhere (model prompts, filename grammar) entirely.
func TestNoUntranslatedText(t *testing.T) {
	problems, _, _ := scanGo(t)
	for _, p := range problems {
		t.Error(p)
	}
}

// TestCatalog fails on a key without a full entry, a translation whose fmt
// verbs differ in number from the key's, two areas translating one key
// differently and (unless narrowed) an entry nothing uses.
func TestCatalog(t *testing.T) {
	for _, k := range conflicts {
		t.Errorf("%q is translated differently by two msg_*.go files", k)
	}
	_, uses, narrowed := scanGo(t)
	used := map[string]bool{}
	for _, u := range uses {
		if _, ok := catalog[u.key]; !ok && !used[u.key] {
			t.Errorf("%s: no catalog entry for %q", u.where, u.key)
		}
		used[u.key] = true
	}
	for key, m := range catalog {
		want := verbs(contextPrefix.ReplaceAllString(key, ""))
		for name, s := range map[string]string{"Hant": m.Hant, "En": m.En, "Ja": m.Ja, "Ko": m.Ko} {
			if strings.TrimSpace(s) == "" {
				t.Errorf("%q has no %s", key, name)
			} else if verbs(s) != want {
				t.Errorf("%q %s has %d fmt verbs, the key %d", key, name, verbs(s), want)
			}
		}
		if m.EnOne != "" && verbs(m.EnOne) != want {
			t.Errorf("%q EnOne has %d fmt verbs, the key %d", key, verbs(m.EnOne), want)
		}
		if !narrowed && !used[key] {
			t.Errorf("%q is not used", key)
		}
	}
}

func TestResolve(t *testing.T) {
	for tags, want := range map[string]Lang{
		"zh-Hans-CN":       SimplifiedChinese,
		"zh-CN":            SimplifiedChinese,
		"zh-SG":            SimplifiedChinese,
		"zh-Hant-TW":       TraditionalChinese,
		"zh-HK":            TraditionalChinese,
		"zh_TW.UTF-8":      TraditionalChinese,
		"zh-Hans-HK":       SimplifiedChinese,
		"fr-FR,ja-JP":      Japanese,
		"ko-KR":            Korean,
		"en-GB,zh-Hans-CN": English,
		"fr-FR,de-DE":      English,
		"":                 English,
	} {
		if got := Resolve(strings.Split(tags, ",")); got != want {
			t.Errorf("Resolve(%q) = %s, want %s", tags, got, want)
		}
	}
}

func TestTranslateFallsBackAndCounts(t *testing.T) {
	saved, savedCatalog := Current(), catalog
	t.Cleanup(func() { current.Store(saved); catalog = savedCatalog })
	catalog = map[string]Msg{
		"已下载 %d 个文件": {Hant: "已下載 %d 個檔案", En: "Downloaded %d files", EnOne: "Downloaded %d file", Ja: "%d 個のファイルをダウンロードしました", Ko: "파일 %d개를 다운로드했습니다"},
		"verb|打开":    {Hant: "打開", En: "Open", Ja: "開く", Ko: "열기"},
	}
	Set(English)
	if got := N(1, "已下载 %d 个文件", 1); got != "Downloaded 1 file" {
		t.Errorf("N(1) = %q", got)
	}
	if got := N(3, "已下载 %d 个文件", 3); got != "Downloaded 3 files" {
		t.Errorf("N(3) = %q", got)
	}
	if got := T("没有的键"); got != "没有的键" {
		t.Errorf("missing key = %q, want the source", got)
	}
	err := New("verb|打开")
	if err.Error() != "Open" {
		t.Errorf("New in English = %q", err)
	}
	Set(SimplifiedChinese)
	if err.Error() != "打开" || T("verb|打开") != "打开" {
		t.Errorf("source text keeps the context prefix: %q", err)
	}
}
