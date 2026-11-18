package i18n

func init() {
	add(map[string]Msg{
		"同步账号需要重新登录":                  {Hant: "同步帳號需要重新登入", En: "The sync account needs to sign in again", Ja: "同期するアカウントへの再ログインが必要です", Ko: "동기화 계정에 다시 로그인해야 합니다"},
		"同步正在等待网络":                    {Hant: "同步正在等待網路", En: "Sync is waiting for a network connection", Ja: "同期はネットワーク接続を待っています", Ko: "동기화가 네트워크 연결을 기다리고 있습니다"},
		"同步没有访问权限":                    {Hant: "同步沒有存取權限", En: "Sync does not have access permission", Ja: "同期に必要なアクセス権がありません", Ko: "동기화 접근 권한이 없습니다"},
		"同步项目不存在":                     {Hant: "同步項目不存在", En: "The sync item does not exist", Ja: "同期項目が存在しません", Ko: "동기화 항목이 없습니다"},
		"同步操作结果待确认，已停止自动重试":           {Hant: "同步操作結果待確認，已停止自動重試", En: "The sync result is unconfirmed; automatic retries have stopped", Ja: "同期操作の結果を確認できないため、自動再試行を停止しました", Ko: "동기화 결과가 확인되지 않아 자동 재시도를 중단했습니다"},
		"同步记录对不上了，请重新扫描并关联文件夹":        {Hant: "同步記錄對不上了，請重新掃描並關聯資料夾", En: "The sync record doesn't match anymore; rescan and link the folders again", Ja: "同期の記録が一致しません。再スキャンしてフォルダを連携し直してください", Ko: "동기화 기록이 일치하지 않습니다. 다시 스캔하고 폴더를 연결하세요"},
		"两边文件又变了，先重新预览确认一下":           {Hant: "兩邊檔案又變了，先重新預覽確認一下", En: "Files changed on disk or cloud; review the preview again", Ja: "ファイルが変更されました。プレビューを確認し直してください", Ko: "파일이 변경되었습니다. 미리보기를 다시 확인하세요"},
		"同步存储或账号接口不可用":                {Hant: "同步儲存或帳號介面無法使用", En: "Sync storage or the account interface is unavailable", Ja: "同期ストレージまたはアカウントのインターフェースを利用できません", Ko: "동기화 저장소 또는 계정 인터페이스를 사용할 수 없습니다"},
		"请选择同步账号和两端文件夹":               {Hant: "請選擇同步帳號和兩端資料夾", En: "Choose a sync account and both folders", Ja: "同期するアカウントと両側のフォルダを選択してください", Ko: "동기화 계정과 양쪽 폴더를 선택하세요"},
		"不支持的文件夹同步模式":                 {Hant: "不支援的資料夾同步模式", En: "Unsupported folder sync mode", Ja: "対応していないフォルダ同期モードです", Ko: "지원하지 않는 폴더 동기화 모드입니다"},
		"云端文件还没处理完，先不同步它":             {Hant: "雲端檔案還沒處理完，先不同步它", En: "The cloud file isn't finished yet, so it's skipped for now", Ja: "クラウドのファイルがまだ処理中のため、今回は同期しません", Ko: "클라우드 파일 처리가 끝나지 않아 아직 동기화하지 않습니다"},
		"目标磁盘不区分大小写或字符编码重名，有文件名冲突":    {Hant: "目標磁碟不區分大小寫或字元編碼重名，有檔名衝突", En: "Filenames collide on the destination disk due to case or Unicode equivalence", Ja: "対象ディスクで大文字・小文字または文字コードによる名前の競合があります", Ko: "대상 디스크에서 대소문자 또는 문자 인코딩으로 인해 파일 이름이 충돌합니다"},
		"同一路径同时存在文件和文件夹":              {Hant: "同一路徑同時存在檔案和資料夾", En: "The same path refers to both a file and a folder", Ja: "同じパスにファイルとフォルダが存在します", Ko: "동일한 경로에 파일과 폴더가 함께 존재합니다"},
		"两端内容不同，保留双方后才能继续":            {Hant: "兩端內容不同，保留雙方後才能繼續", En: "The contents differ on each side; keep both copies before continuing", Ja: "両側の内容が異なります。両方を残してから続行してください", Ko: "양쪽 내용이 다릅니다. 두 사본을 모두 유지한 후 계속할 수 있습니다"},
		"保留仅存在于目标端的文件":                {Hant: "保留僅存在於目標端的檔案", En: "Keep extra files that only exist on the destination side", Ja: "同期先にのみ存在する追加ファイルを残します", Ko: "대상에만 있는 추가 파일을 유지합니다"},
		"这边删了，但另一端后来又改过，先确认下留哪边":      {Hant: "這邊刪了，但另一端後來又改過，先確認下留哪邊", En: "Deleted on one side, then edited on the other; confirm what to keep", Ja: "片側で削除後に反対側が編集されました。残す方を確認してください", Ko: "한쪽에서 삭제한 뒤 다른 쪽이 수정되었습니다. 무엇을 남길지 확인하세요"},
		"删过的文件又出现了，先核对下两边内容":          {Hant: "刪過的檔案又出現了，先核對下兩邊內容", En: "A deleted item came back; check both sides first", Ja: "削除後に再び現れた項目があります。両側の内容を確認してください", Ko: "삭제했던 항목이 다시 나타났습니다. 양쪽 내용을 확인하세요"},
		"目标文件改过了，先保留目标端，不覆盖回源端":       {Hant: "目標檔案改過了，先保留目標端，不覆蓋回來源端", En: "The destination copy was edited; keeping it without overwriting the source", Ja: "同期先が変更されているため、そのまま残して同期元へは書き戻しません", Ko: "대상이 수정되어 원본에 덮어쓰지 않고 대상을 유지합니다"},
		"两端均有变化，或删除与编辑冲突":             {Hant: "兩端均有變化，或刪除與編輯衝突", En: "Both sides changed, or a deletion conflicts with an edit", Ja: "両側に変更があるか、削除と編集が競合しています", Ko: "양쪽이 변경되었거나 삭제와 수정이 충돌합니다"},
		"已记住这次删除并保留了另一端，不会自动恢复源端":     {Hant: "已記住這次刪除並保留了另一端，不會自動恢復來源端", En: "Remembered this deletion and kept the other copy; the source won't come back automatically", Ja: "削除を記録して反対側を残しました。同期元は自動で戻しません", Ko: "이 삭제를 기록하고 반대쪽 사본을 유지했으며 원본은 자동으로 복구하지 않습니다"},
		"这批要删的内容比较多，触发了防误删保护，请确认后再继续": {Hant: "這批要刪的內容比較多，觸發了防誤刪保護，請確認後再繼續", En: "This batch removes a lot at once; confirm the deletions before continuing", Ja: "削除数が多いため誤削除防止が作動しました。内容を確認してから続行してください", Ko: "삭제할 항목이 많아 실수 방지 보호가 켜졌습니다. 확인한 뒤 계속하세요"},
		"本地同步文件夹和现有的重复了：%s":           {Hant: "本機同步資料夾和現有的重複了：%s", En: "This local folder overlaps an existing sync pair: %s", Ja: "ローカルの同期フォルダが既存の設定と重複しています：%s", Ko: "로컬 동기화 폴더가 기존 동기화와 겹칩니다: %s"},
		"云端同步文件夹和现有的重复了：%s":           {Hant: "雲端同步資料夾和現有的重複了：%s", En: "This cloud folder overlaps an existing sync pair: %s", Ja: "クラウドの同期フォルダが既存の設定と重複しています：%s", Ko: "클라우드 동기화 폴더가 기존 동기화와 겹칩니다: %s"},
		"云端有重名文件，没法一一对应：%s":           {Hant: "雲端有重名檔案，沒法一一對應：%s", En: "Duplicate cloud names can't be matched cleanly: %s", Ja: "クラウドに同名の項目が複数あるため対応付けできません：%s", Ko: "클라우드에 같은 이름의 항목이 있어 정확히 대응할 수 없습니다: %s"},
	})
}
