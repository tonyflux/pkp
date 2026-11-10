package i18n

func init() {
	add(map[string]Msg{
		"助手操作参数无效，未处理":               {Hant: "助手操作參數無效，未處理", En: "Invalid assistant operation parameters; nothing was processed", Ja: "アシスタント操作の引数が無効です。処理は行っていません", Ko: "도우미 작업 매개변수가 잘못되어 처리하지 않았습니다"},
		"助手不支持此操作，未处理":               {Hant: "助手不支援此操作，未處理", En: "The assistant does not support this operation; nothing was processed", Ja: "アシスタントはこの操作に対応していません。処理は行っていません", Ko: "도우미가 지원하지 않는 작업이므로 처리하지 않았습니다"},
		"目标已失效或不属于此类操作，未处理":          {Hant: "目標已失效或不屬於此類操作，未處理", En: "The target is no longer valid or does not belong to this operation type; nothing was processed", Ja: "対象が無効になったか、この種類の操作の対象ではありません。処理は行っていません", Ko: "대상이 더 이상 유효하지 않거나 이 작업 유형에 속하지 않아 처리하지 않았습니다"},
		"目标状态已改变，不适用此操作，未处理":         {Hant: "目標狀態已改變，不適用此操作，未處理", En: "The target state changed and this operation no longer applies; nothing was processed", Ja: "対象の状態が変わり、この操作を適用できません。処理は行っていません", Ko: "대상 상태가 변경되어 이 작업을 적용할 수 없으므로 처리하지 않았습니다"},
		"不能把文件夹复制到自身或其子文件夹":          {Hant: "不能把資料夾複製到自身或其子資料夾", En: "A folder cannot be copied into itself or its subfolders", Ja: "フォルダをそのフォルダ自身や子フォルダにコピーすることはできません", Ko: "폴더를 자기 자신이나 하위 폴더로 복사할 수 없습니다"},
		"此云端任务没有可用的种子哈希，不能重试":        {Hant: "此雲端任務沒有可用的種子雜湊，不能重試", En: "This cloud task has no usable torrent hash and cannot be retried", Ja: "このクラウドタスクには使用可能なトレントハッシュがないため、再試行できません", Ko: "이 클라우드 작업에 사용할 수 있는 토렌트 해시가 없어 다시 시도할 수 없습니다"},
		"操作未能完成，敏感错误详情已隐藏":           {Hant: "操作未能完成，敏感錯誤詳情已隱藏", En: "The operation could not complete; sensitive error details are hidden", Ja: "操作を完了できませんでした。機密情報を含むエラー詳細は非表示にしています", Ko: "작업을 완료하지 못했으며 민감한 오류 세부 정보는 숨겼습니다"},
		"已隐藏敏感信息":                    {Hant: "已隱藏敏感資訊", En: "Sensitive information hidden", Ja: "機密情報は非表示", Ko: "민감한 정보 숨김"},
		"云盘拒绝了操作（状态码 %d），敏感错误详情已隐藏":  {Hant: "雲端硬碟拒絕了操作（狀態碼 %d），敏感錯誤詳情已隱藏", En: "The cloud drive rejected the operation (status %d); sensitive error details are hidden", Ja: "クラウドドライブが操作を拒否しました（ステータスコード %d）。機密情報を含むエラー詳細は非表示にしています", Ko: "클라우드 드라이브가 작업을 거부했으며(상태 코드 %d) 민감한 오류 세부 정보는 숨겼습니다"},
		"云盘返回的结果页过大，无法安全分页，未返回不完整结果": {Hant: "雲端硬碟傳回的結果頁過大，無法安全分頁，未傳回不完整結果", En: "The cloud drive returned an oversized page; safe pagination was not possible and no incomplete results were returned", Ja: "クラウドドライブが返したページが大きすぎるため、安全にページ分割できません。不完全な結果は返していません", Ko: "클라우드 드라이브가 반환한 페이지가 너무 커서 안전하게 나눌 수 없으며 불완전한 결과는 반환하지 않았습니다"},
	})
}
