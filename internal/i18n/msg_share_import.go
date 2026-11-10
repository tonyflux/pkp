package i18n

func init() {
	add(map[string]Msg{
		"分享链接或文件选择无效":             {Hant: "分享連結或檔案選擇無效", En: "Invalid share link or file selection", Ja: "共有リンクまたはファイルの選択が無効です", Ko: "공유 링크 또는 파일 선택이 올바르지 않습니다"},
		"分享已失效或无法访问，请重新检查链接和提取码":  {Hant: "分享已失效或無法存取，請重新檢查連結和提取碼", En: "This share expired or is unavailable. Check the link and access code.", Ja: "共有の有効期限が切れているかアクセスできません。リンクとアクセスコードを確認してください。", Ko: "공유가 만료되었거나 접근할 수 없습니다. 링크와 액세스 코드를 확인하세요."},
		"此分享文件不支持预览":              {Hant: "此分享檔案不支援預覽", En: "This shared file cannot be previewed", Ja: "この共有ファイルはプレビューできません", Ko: "이 공유 파일은 미리 볼 수 없습니다"},
		"无法确认转存结果，请检查目标文件夹，勿重复提交": {Hant: "無法確認轉存結果，請檢查目標資料夾，勿重複提交", En: "The restore outcome is unconfirmed. Check the destination folder; do not submit again.", Ja: "保存結果を確認できません。保存先フォルダーを確認し、再送信しないでください。", Ko: "저장 결과를 확인할 수 없습니다. 대상 폴더를 확인하고 다시 제출하지 마세요."},
		"转存结果与所选文件或目标文件夹不一致":      {Hant: "轉存結果與所選檔案或目標資料夾不一致", En: "The restore result does not match the selected files or destination folder", Ja: "保存結果が選択したファイルまたは保存先フォルダーと一致しません", Ko: "저장 결과가 선택한 파일 또는 대상 폴더와 일치하지 않습니다"},
		"未找到此账号的分享转存任务":           {Hant: "未找到此帳號的分享轉存任務", En: "No matching share restore task was found for this account", Ja: "このアカウントの共有保存タスクが見つかりません", Ko: "이 계정의 공유 저장 작업을 찾을 수 없습니다"},
		"分享转存失败":                  {Hant: "分享轉存失敗", En: "Share restore failed", Ja: "共有ファイルの保存に失敗しました", Ko: "공유 파일 저장에 실패했습니다"},
		"待确认的转存任务过多，请先检查目标文件夹":    {Hant: "待確認的轉存任務過多，請先檢查目標資料夾", En: "Too many restores are awaiting confirmation. Check the destination folders first.", Ja: "確認待ちの保存タスクが多すぎます。先に保存先フォルダーを確認してください。", Ko: "확인을 기다리는 저장 작업이 너무 많습니다. 먼저 대상 폴더를 확인하세요."},
	})
}
