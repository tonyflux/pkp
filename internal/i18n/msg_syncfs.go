package i18n

func init() {
	add(map[string]Msg{
		"同步的根目录对不上了，请重新关联":            {Hant: "同步的根目錄對不上了，請重新關聯", En: "The sync root folder no longer matches. Link the folders again.", Ja: "同期ルートフォルダが一致しなくなりました。フォルダを連携し直してください。", Ko: "동기화 루트 폴더가 일치하지 않습니다. 폴더를 다시 연결하세요."},
		"同步磁盘或目录不可用":                  {Hant: "同步磁碟或目錄無法使用", En: "The sync disk or directory is unavailable", Ja: "同期ディスクまたはフォルダを利用できません", Ko: "동기화 디스크 또는 폴더를 사용할 수 없습니다"},
		"没有同步目录的访问权限":                 {Hant: "沒有同步目錄的存取權限", En: "Access to the sync directory is denied", Ja: "同期フォルダへのアクセス権がありません", Ko: "동기화 폴더에 대한 접근 권한이 없습니다"},
		"当前磁盘的文件系统不支持可靠同步":            {Hant: "目前磁碟的檔案系統不支援可靠同步", En: "This disk's filesystem doesn't support reliable sync", Ja: "このディスクのファイルシステムは安全な同期に対応していません", Ko: "이 디스크의 파일 시스템은 안정적인 동기화를 지원하지 않습니다"},
		"同步路径里包含符号链接或不太安全，没法同步":       {Hant: "同步路徑包含符號連結或不太安全，無法同步", En: "The sync path isn't safe or contains a symbolic link", Ja: "同期パスにシンボリックリンクが含まれるか安全ではないため同期できません", Ko: "동기화 경로에 심볼릭 링크가 포함되었거나 안전하지 않습니다"},
		"文件正在变化，请稍后重新扫描":              {Hant: "檔案正在變更，請稍後重新掃描", En: "Files are changing. Scan again shortly.", Ja: "ファイルが変更されています。しばらくしてから再スキャンしてください。", Ko: "파일이 변경 중입니다. 잠시 후 다시 검색하세요."},
		".pkp-sync 已被用户文件占用，不能用于同步暂存": {Hant: ".pkp-sync 已被使用者檔案占用，不能用於同步暫存", En: ".pkp-sync is occupied by user files and cannot be used for sync staging", Ja: ".pkp-sync はユーザーのファイルで使用中のため、同期待機領域に使えません", Ko: ".pkp-sync가 사용자 파일에 사용되고 있어 동기화 임시 저장소로 사용할 수 없습니다"},
		"文件名与现有名称冲突":                  {Hant: "檔案名稱與現有名稱衝突", En: "The filename conflicts with an existing name", Ja: "ファイル名が既存の名前と競合しています", Ko: "파일 이름이 기존 이름과 충돌합니다"},
		"文件名超出平台长度限制：%s":              {Hant: "檔案名稱超出平台長度限制：%s", En: "The filename exceeds the platform length limit: %s", Ja: "ファイル名がプラットフォームの長さ制限を超えています：%s", Ko: "파일 이름이 플랫폼 길이 제한을 초과합니다: %s"},
		"Windows 无法表示此文件名：%s":         {Hant: "Windows 無法表示此檔案名稱：%s", En: "Windows cannot represent this filename: %s", Ja: "Windows ではこのファイル名を使用できません：%s", Ko: "Windows에서 이 파일 이름을 사용할 수 없습니다: %s"},
		"同步路径超出平台长度限制：%s":             {Hant: "同步路徑超出平台長度限制：%s", En: "The sync path exceeds the platform length limit: %s", Ja: "同期パスがプラットフォームの長さ制限を超えています：%s", Ko: "동기화 경로가 플랫폼 길이 제한을 초과합니다: %s"},
		"符号链接不会参与同步":                  {Hant: "符號連結不會參與同步", En: "Symbolic links are not synced", Ja: "シンボリックリンクは同期されません", Ko: "심볼릭 링크는 동기화하지 않습니다"},
		"临时文件或系统文件":                   {Hant: "暫存檔或系統檔", En: "Temporary or system file", Ja: "一時ファイルまたはシステムファイル", Ko: "임시 파일 또는 시스템 파일"},
		"不支持同步此特殊文件":                  {Hant: "不支援同步此特殊檔案", En: "This special file cannot be synced", Ja: "この特殊ファイルは同期できません", Ko: "이 특수 파일은 동기화할 수 없습니다"},
	})
}
