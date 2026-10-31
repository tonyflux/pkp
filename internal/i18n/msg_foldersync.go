package i18n

func init() {
	add(map[string]Msg{
		"同步账号需要重新登录":                 {Hant: "同步帳號需要重新登入", En: "The sync account needs to sign in again", Ja: "同期するアカウントへの再ログインが必要です", Ko: "동기화 계정에 다시 로그인해야 합니다"},
		"同步正在等待网络":                   {Hant: "同步正在等待網路", En: "Sync is waiting for a network connection", Ja: "同期はネットワーク接続を待っています", Ko: "동기화가 네트워크 연결을 기다리고 있습니다"},
		"同步没有访问权限":                   {Hant: "同步沒有存取權限", En: "Sync does not have access permission", Ja: "同期に必要なアクセス権がありません", Ko: "동기화 접근 권한이 없습니다"},
		"同步项目不存在":                    {Hant: "同步項目不存在", En: "The sync item does not exist", Ja: "同期項目が存在しません", Ko: "동기화 항목이 없습니다"},
		"同步操作结果待确认，已停止自动重试":          {Hant: "同步操作結果待確認，已停止自動重試", En: "The sync result is unconfirmed; automatic retries have stopped", Ja: "同期操作の結果を確認できないため、自動再試行を停止しました", Ko: "동기화 결과가 확인되지 않아 자동 재시도를 중단했습니다"},
		"同步基线或根身份无法验证，请重新扫描并配对":      {Hant: "同步基準或根目錄身分無法驗證，請重新掃描並配對", En: "The sync baseline or root identity cannot be verified; scan and pair again", Ja: "同期の基準またはルートの識別情報を検証できません。再スキャンしてペアリングしてください", Ko: "동기화 기준 또는 루트 식별 정보를 확인할 수 없습니다. 다시 스캔하고 연결하세요"},
		"同步内容已经变化，请重新审阅预览":           {Hant: "同步內容已經變化，請重新審閱預覽", En: "The sync contents have changed; review the preview again", Ja: "同期内容が変更されました。プレビューを確認し直してください", Ko: "동기화 내용이 변경되었습니다. 미리보기를 다시 검토하세요"},
		"同步存储或账号接口不可用":               {Hant: "同步儲存或帳號介面無法使用", En: "Sync storage or the account interface is unavailable", Ja: "同期ストレージまたはアカウントのインターフェースを利用できません", Ko: "동기화 저장소 또는 계정 인터페이스를 사용할 수 없습니다"},
		"请选择同步账号和两端文件夹":              {Hant: "請選擇同步帳號和兩端資料夾", En: "Choose a sync account and both folders", Ja: "同期するアカウントと両側のフォルダを選択してください", Ko: "동기화 계정과 양쪽 폴더를 선택하세요"},
		"不支持的文件夹同步模式":                {Hant: "不支援的資料夾同步模式", En: "Unsupported folder sync mode", Ja: "対応していないフォルダ同期モードです", Ko: "지원하지 않는 폴더 동기화 모드입니다"},
		"云端文件尚未完成，暂不参与同步":            {Hant: "雲端檔案尚未完成，暫不參與同步", En: "The cloud file is incomplete and will not sync yet", Ja: "クラウドのファイルが未完成のため、まだ同期しません", Ko: "클라우드 파일이 아직 완료되지 않아 동기화하지 않습니다"},
		"文件名在目标磁盘上发生大小写或 Unicode 冲突": {Hant: "檔名在目標磁碟上發生大小寫或 Unicode 衝突", En: "Filenames collide on the destination disk due to case or Unicode equivalence", Ja: "対象ディスクで大文字・小文字または Unicode の等価性による名前の競合があります", Ko: "대상 디스크에서 대소문자 또는 Unicode 동등성으로 파일 이름이 충돌합니다"},
		"同一路径同时存在文件和文件夹":             {Hant: "同一路徑同時存在檔案和資料夾", En: "The same path refers to both a file and a folder", Ja: "同じパスにファイルとフォルダが存在します", Ko: "동일한 경로에 파일과 폴더가 함께 존재합니다"},
		"两端内容不同，保留双方后才能继续":           {Hant: "兩端內容不同，保留雙方後才能繼續", En: "The contents differ on each side; preserve both before continuing", Ja: "両側の内容が異なります。両方を保持してから続行してください", Ko: "양쪽 내용이 다릅니다. 두 버전을 보존한 후 계속할 수 있습니다"},
		"保留单向同步目标端的额外项目":             {Hant: "保留單向同步目標端的額外項目", En: "Keep additional items on the one-way sync destination", Ja: "一方向同期の同期先にある追加項目を保持します", Ko: "단방향 동기화 대상의 추가 항목을 보존합니다"},
		"删除后保留的另一端又有编辑，请确认保留结果":      {Hant: "刪除後保留的另一端又有編輯，請確認保留結果", En: "The copy preserved after deletion was edited; confirm what to keep", Ja: "削除後に保持した側が編集されています。保持する内容を確認してください", Ko: "삭제 후 보존한 사본이 수정되었습니다. 보존 결과를 확인하세요"},
		"删除后重新出现的项目需要核对双方内容":         {Hant: "刪除後重新出現的項目需要核對雙方內容", En: "An item reappeared after deletion; verify the contents on both sides", Ja: "削除後に項目が再び現れました。両側の内容を確認してください", Ko: "삭제 후 항목이 다시 나타났습니다. 양쪽 내용을 확인하세요"},
		"目标端已被修改，保留目标且不写回源端":         {Hant: "目標端已被修改，保留目標且不寫回來源端", En: "The destination was edited; preserve it without writing back to the source", Ja: "同期先が編集されています。同期先を保持し、同期元へは書き戻しません", Ko: "대상이 수정되었습니다. 대상을 보존하고 원본에 다시 쓰지 않습니다"},
		"两端均有变化，或删除与编辑冲突":            {Hant: "兩端均有變化，或刪除與編輯衝突", En: "Both sides changed, or a deletion conflicts with an edit", Ja: "両側に変更があるか、削除と編集が競合しています", Ko: "양쪽이 변경되었거나 삭제와 수정이 충돌합니다"},
		"已记录删除并保留另一端，不自动恢复源端":        {Hant: "已記錄刪除並保留另一端，不自動還原來源端", En: "Deletion recorded and the other copy preserved; the source will not be restored automatically", Ja: "削除を記録して反対側を保持しました。同期元を自動で復元しません", Ko: "삭제를 기록하고 다른 사본을 보존했습니다. 원본을 자동으로 복원하지 않습니다"},
		"本批删除触发保护，请审阅并确认当前版本":        {Hant: "本批刪除觸發保護，請審閱並確認目前版本", En: "This deletion batch triggered protection; review and confirm the current versions", Ja: "この一括削除は保護の対象です。現在のバージョンを確認して承認してください", Ko: "이 삭제 묶음에 보호가 적용되었습니다. 현재 버전을 검토하고 승인하세요"},
		"本地同步目录与已有关系重叠：%s":           {Hant: "本機同步目錄與現有關係重疊：%s", En: "The local sync folder overlaps an existing pair: %s", Ja: "ローカルの同期フォルダが既存のペアと重複しています：%s", Ko: "로컬 동기화 폴더가 기존 연결과 겹칩니다: %s"},
		"云端同步目录与已有关系重叠：%s":           {Hant: "雲端同步目錄與現有關係重疊：%s", En: "The cloud sync folder overlaps an existing pair: %s", Ja: "クラウドの同期フォルダが既存のペアと重複しています：%s", Ko: "클라우드 동기화 폴더가 기존 연결과 겹칩니다: %s"},
		"云端存在重复名称，无法安全映射：%s":         {Hant: "雲端存在重複名稱，無法安全對應：%s", En: "Duplicate cloud names cannot be mapped safely: %s", Ja: "クラウドに同じ名前が複数あるため、安全に対応付けできません：%s", Ko: "클라우드 이름이 중복되어 안전하게 연결할 수 없습니다: %s"},
	})
}
