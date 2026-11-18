package i18n

func init() {
	add(map[string]Msg{
		"操作参数有误，没有执行":               {Hant: "操作參數有誤，沒有執行", En: "Invalid action parameters; nothing ran", Ja: "操作の引数が正しくないため、実行しませんでした", Ko: "작업 매개변수가 올바르지 않아 실행하지 않았습니다"},
		"暂时不支持这个操作":                 {Hant: "暫時不支援這項操作", En: "This action isn't supported yet", Ja: "この操作にはまだ対応していません", Ko: "이 작업은 아직 지원하지 않습니다"},
		"找不到对应文件，或无法执行此操作":         {Hant: "找不到對應檔案，或無法執行此操作", En: "Target is missing or doesn't support this action", Ja: "対象が見つからないか、この操作を行えません", Ko: "대상이 없거나 이 작업을 적용할 수 없습니다"},
		"文件状态已变更，当前无法执行此操作":          {Hant: "檔案狀態已變更，目前無法執行此操作", En: "Item state changed; this action no longer applies", Ja: "状態が変わったため、この操作は行えません", Ko: "상태가 바뀌어 이 작업을 적용할 수 없습니다"},
		"不能把文件夹复制到自身或其子文件夹":         {Hant: "不能把資料夾複製到自身或其子資料夾", En: "A folder cannot be copied into itself or its subfolders", Ja: "フォルダをそのフォルダ自身や子フォルダにコピーすることはできません", Ko: "폴더를 자기 자신이나 하위 폴더로 복사할 수 없습니다"},
		"此云端任务没有可用的种子哈希，不能重试":       {Hant: "此雲端任務沒有可用的種子雜湊，不能重試", En: "This cloud task has no usable torrent hash and cannot be retried", Ja: "このクラウドタスクには使用可能なトレントハッシュがないため、再試行できません", Ko: "이 클라우드 작업에 사용할 수 있는 토렌트 해시가 없어 다시 시도할 수 없습니다"},
		"操作未能完成（已隐藏敏感错误详情）":         {Hant: "操作未能完成（已隱藏敏感錯誤詳情）", En: "Action couldn't finish (sensitive error details hidden)", Ja: "操作を完了できませんでした（プライバシー保護のため詳細は非表示）", Ko: "작업을 완료하지 못했습니다(민감한 오류 세부 정보 숨김)"},
		"已隐藏敏感信息":                   {Hant: "已隱藏敏感資訊", En: "Sensitive information hidden", Ja: "機密情報は非表示", Ko: "민감한 정보 숨김"},
		"云盘拒绝了这个操作（状态码 %d，已隐藏敏感详情）": {Hant: "雲端硬碟拒絕了這項操作（狀態碼 %d，已隱藏敏感詳情）", En: "The drive refused this action (status %d; sensitive details hidden)", Ja: "クラウドドライブがこの操作を拒否しました（ステータスコード %d、詳細は非表示）", Ko: "클라우드 드라이브가 작업을 거부했습니다(상태 코드 %d, 민감한 정보 숨김)"},
		"云盘本页返回数据过大，无法继续读取":       {Hant: "雲端硬碟本頁傳回資料過大，無法繼續讀取", En: "The drive returned too much data in one page to read further", Ja: "クラウドドライブから返されたページが大きすぎて読み進められません", Ko: "클라우드 드라이브가 한 번에 반환한 페이지가 너무 커서 더 읽을 수 없습니다"},
	})
}
