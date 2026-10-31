package i18n

func init() {
	add(map[string]Msg{
		"同步根目录身份已改变，请重新配对":            {Hant: "同步根目錄身分已改變，請重新配對", En: "The sync root identity changed. Pair the folders again.", Ja: "同期ルートの識別情報が変わりました。再度ペア設定してください。", Ko: "동기화 루트의 식별 정보가 변경되었습니다. 폴더를 다시 연결하세요."},
		"同步磁盘或目录不可用":                  {Hant: "同步磁碟或目錄無法使用", En: "The sync disk or directory is unavailable", Ja: "同期ディスクまたはフォルダを利用できません", Ko: "동기화 디스크 또는 폴더를 사용할 수 없습니다"},
		"没有同步目录的访问权限":                 {Hant: "沒有同步目錄的存取權限", En: "Access to the sync directory is denied", Ja: "同期フォルダへのアクセス権がありません", Ko: "동기화 폴더에 대한 접근 권한이 없습니다"},
		"此文件系统不支持安全同步操作":              {Hant: "此檔案系統不支援安全同步操作", En: "This filesystem does not support safe sync operations", Ja: "このファイルシステムは安全な同期操作に対応していません", Ko: "이 파일 시스템은 안전한 동기화 작업을 지원하지 않습니다"},
		"同步路径不安全或包含符号链接":              {Hant: "同步路徑不安全或包含符號連結", En: "The sync path is unsafe or contains a symbolic link", Ja: "同期パスが安全でないか、シンボリックリンクを含んでいます", Ko: "동기화 경로가 안전하지 않거나 심볼릭 링크를 포함합니다"},
		"文件正在变化，请稍后重新扫描":              {Hant: "檔案正在變更，請稍後重新掃描", En: "Files are changing. Scan again shortly.", Ja: "ファイルが変更されています。しばらくしてから再スキャンしてください。", Ko: "파일이 변경 중입니다. 잠시 후 다시 검색하세요."},
		".pkp-sync 已被用户文件占用，不能用于同步暂存": {Hant: ".pkp-sync 已被使用者檔案占用，不能用於同步暫存", En: ".pkp-sync is occupied by user files and cannot be used for sync staging", Ja: ".pkp-sync はユーザーのファイルで使用中のため、同期待機領域に使えません", Ko: ".pkp-sync가 사용자 파일에 사용되고 있어 동기화 임시 저장소로 사용할 수 없습니다"},
		"文件名与现有名称冲突":                  {Hant: "檔案名稱與現有名稱衝突", En: "The filename conflicts with an existing name", Ja: "ファイル名が既存の名前と競合しています", Ko: "파일 이름이 기존 이름과 충돌합니다"},
		"文件名超出平台长度限制：%s":              {Hant: "檔案名稱超出平台長度限制：%s", En: "The filename exceeds the platform length limit: %s", Ja: "ファイル名がプラットフォームの長さ制限を超えています：%s", Ko: "파일 이름이 플랫폼 길이 제한을 초과합니다: %s"},
		"Windows 无法表示此文件名：%s":         {Hant: "Windows 無法表示此檔案名稱：%s", En: "Windows cannot represent this filename: %s", Ja: "Windows ではこのファイル名を使用できません：%s", Ko: "Windows에서 이 파일 이름을 사용할 수 없습니다: %s"},
		"同步路径超出平台长度限制：%s":             {Hant: "同步路徑超出平台長度限制：%s", En: "The sync path exceeds the platform length limit: %s", Ja: "同期パスがプラットフォームの長さ制限を超えています：%s", Ko: "동기화 경로가 플랫폼 길이 제한을 초과합니다: %s"},
		"符号链接不会参与同步":                  {Hant: "符號連結不會參與同步", En: "Symbolic links are not synced", Ja: "シンボリックリンクは同期されません", Ko: "심볼릭 링크는 동기화하지 않습니다"},
		"已识别的临时或系统控制文件":               {Hant: "已識別的暫存或系統控制檔案", En: "Recognized temporary or system control file", Ja: "既知の一時ファイルまたはシステム制御ファイル", Ko: "알려진 임시 또는 시스템 제어 파일"},
		"不支持同步此特殊文件":                  {Hant: "不支援同步此特殊檔案", En: "This special file cannot be synced", Ja: "この特殊ファイルは同期できません", Ko: "이 특수 파일은 동기화할 수 없습니다"},
	})
}
