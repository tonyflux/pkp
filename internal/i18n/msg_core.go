// Messages of Interface language itself; see i18n.go.

package i18n

func init() {
	add(map[string]Msg{
		"不支持的界面语言：%s": {Hant: "不支援的介面語言：%s", En: "Unsupported language: %s", Ja: "サポートされていない言語です：%s", Ko: "지원하지 않는 언어입니다: %s"},
	})
}
