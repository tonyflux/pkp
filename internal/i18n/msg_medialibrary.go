package i18n

func init() {
	add(map[string]Msg{
		"媒体库本地记录版本不受支持": {Hant: "媒體庫本機記錄版本不受支援", En: "The local media library record version is unsupported", Ja: "ローカルのライブラリ記録のバージョンには対応していません", Ko: "로컬 미디어 라이브러리 기록 버전을 지원하지 않습니다"},
		"媒体库分组信息无效":     {Hant: "媒體庫分組資訊無效", En: "Invalid media library grouping", Ja: "ライブラリの分類情報が無効です", Ko: "미디어 라이브러리 그룹 정보가 잘못되었습니다"},
	})
}
