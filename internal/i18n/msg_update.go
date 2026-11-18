package i18n

func init() {
	add(map[string]Msg{
		"检查更新…": {Hant: "檢查更新…", En: "Check for Updates…", Ja: "アップデートを確認…", Ko: "업데이트 확인…"},
		"无法保存更新文件或设置，请检查磁盘空间与目录权限。": {Hant: "無法儲存更新檔案或設定，請檢查磁碟空間與資料夾權限。", En: "Cannot save update files or settings. Check disk space and folder permissions.", Ja: "更新ファイルまたは設定を保存できません。空き容量とフォルダの権限を確認してください。", Ko: "업데이트 파일 또는 설정을 저장할 수 없습니다. 디스크 공간과 폴더 권한을 확인하세요."},
		"当前版本不支持在应用里直接更新":           {Hant: "目前版本不支援在應用程式內直接更新", En: "In-app updates aren't available in this build", Ja: "このビルドではアプリ内更新を利用できません", Ko: "이 빌드에서는 앱 내 업데이트를 사용할 수 없습니다"},
		"已经在更新中了":     {Hant: "已經在更新中了", En: "An update is already in progress", Ja: "更新がすでに進行中です", Ko: "업데이트가 이미 진행 중입니다"},
		"没有适合当前系统的更新": {Hant: "沒有適合目前系統的更新", En: "No compatible update is available for this system", Ja: "このシステムに対応する更新はありません", Ko: "이 시스템에 맞는 업데이트가 없습니다"},
		"请先下载并校验更新":   {Hant: "請先下載並驗證更新", En: "Download and verify the update first", Ja: "先に更新をダウンロードして検証してください", Ko: "먼저 업데이트를 다운로드하고 검증하세요"},
	})
}
