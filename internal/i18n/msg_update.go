package i18n

func init() {
	add(map[string]Msg{
		"检查更新…": {Hant: "檢查更新…", En: "Check for Updates…", Ja: "アップデートを確認…", Ko: "업데이트 확인…"},
		"无法保存更新文件或设置，请检查磁盘空间与目录权限。": {Hant: "無法儲存更新檔案或設定，請檢查磁碟空間與目錄權限。", En: "Cannot save update files or settings. Check disk space and folder permissions.", Ja: "更新ファイルまたは設定を保存できません。空き容量とフォルダの権限を確認してください。", Ko: "업데이트 파일 또는 설정을 저장할 수 없습니다. 디스크 공간과 폴더 권한을 확인하세요."},
		"此构建不支持应用内更新":               {Hant: "此版本不支援應用程式內更新", En: "In-app updates are unavailable in this build", Ja: "このビルドではアプリ内更新を利用できません", Ko: "이 빌드에서는 앱 내 업데이트를 사용할 수 없습니다"},
		"更新操作正在进行":                  {Hant: "更新操作正在進行", En: "An update operation is already in progress", Ja: "更新操作が進行中です", Ko: "업데이트 작업이 진행 중입니다"},
		"没有适用于此系统的更新":               {Hant: "沒有適用於此系統的更新", En: "No compatible update is available for this system", Ja: "このシステムに対応する更新はありません", Ko: "이 시스템에 호환되는 업데이트가 없습니다"},
		"请先下载并校验更新":                 {Hant: "請先下載並驗證更新", En: "Download and verify the update first", Ja: "先に更新をダウンロードして検証してください", Ko: "먼저 업데이트를 다운로드하고 검증하세요"},
	})
}
