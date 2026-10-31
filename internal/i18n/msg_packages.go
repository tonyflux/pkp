// Messages of packages; see i18n.go.

package i18n

func init() {
	add(map[string]Msg{
		// applock
		"这台 Mac 没有设置登录密码，无法使用系统身份验证": {Hant: "這台 Mac 沒有設定登入密碼，無法使用系統驗證", En: "This Mac has no login password, so system authentication can't be used", Ja: "この Mac にはログインパスワードが設定されていないため、システム認証を使用できません", Ko: "이 Mac에 로그인 암호가 설정되어 있지 않아 시스템 인증을 사용할 수 없습니다"},
		"已取消验证":                             {Hant: "已取消驗證", En: "Authentication cancelled", Ja: "認証がキャンセルされました", Ko: "인증이 취소되었습니다"},
		"身份验证没有通过":                          {Hant: "驗證未通過", En: "Authentication failed", Ja: "認証に失敗しました", Ko: "인증에 실패했습니다"},
		"系统身份验证不可用":                         {Hant: "系統驗證無法使用", En: "System authentication is unavailable", Ja: "システム認証を使用できません", Ko: "시스템 인증을 사용할 수 없습니다"},
		"这个系统上的 PKP 还不能使用系统身份验证":            {Hant: "此系統上的 PKP 尚不支援系統驗證", En: "PKP can't use system authentication on this system yet", Ja: "このシステムの PKP はまだシステム認証に対応していません", Ko: "이 시스템의 PKP는 아직 시스템 인증을 사용할 수 없습니다"},
		"无法使用 Windows Hello（%v）":            {Hant: "無法使用 Windows Hello（%v）", En: "Can't use Windows Hello (%v)", Ja: "Windows Hello を使用できません（%v）", Ko: "Windows Hello를 사용할 수 없습니다(%v)"},
		"这台电脑没有可用的 Windows Hello 设备":        {Hant: "這台電腦沒有可用的 Windows Hello 裝置", En: "This computer has no Windows Hello device available", Ja: "このコンピュータには使用できる Windows Hello デバイスがありません", Ko: "이 컴퓨터에는 사용할 수 있는 Windows Hello 장치가 없습니다"},
		"这台电脑没有设置 Windows Hello（PIN、指纹或面部）": {Hant: "這台電腦沒有設定 Windows Hello（PIN、指紋或臉部）", En: "Windows Hello (PIN, fingerprint or face) isn't set up on this computer", Ja: "このコンピュータでは Windows Hello（PIN、指紋、顔）が設定されていません", Ko: "이 컴퓨터에 Windows Hello(PIN, 지문 또는 얼굴)가 설정되어 있지 않습니다"},
		"Windows Hello 已被系统策略停用":            {Hant: "Windows Hello 已被系統原則停用", En: "Windows Hello is disabled by system policy", Ja: "Windows Hello はシステムポリシーで無効になっています", Ko: "Windows Hello가 시스템 정책으로 비활성화되었습니다"},
		"Windows Hello 不可用（状态 %d）":          {Hant: "Windows Hello 無法使用（狀態 %d）", En: "Windows Hello is unavailable (status %d)", Ja: "Windows Hello を使用できません（状態 %d）", Ko: "Windows Hello를 사용할 수 없습니다(상태 %d)"},
		"Windows Hello 正忙，请稍后再试":            {Hant: "Windows Hello 忙碌中，請稍後再試", En: "Windows Hello is busy. Try again later", Ja: "Windows Hello は使用中です。しばらくしてからもう一度お試しください", Ko: "Windows Hello가 사용 중입니다. 잠시 후 다시 시도하세요"},
		"验证失败次数过多":                          {Hant: "驗證失敗次數過多", En: "Too many failed attempts", Ja: "認証の失敗回数が多すぎます", Ko: "인증 실패 횟수가 너무 많습니다"},
		"Windows Hello 返回了未知结果（%d）":         {Hant: "Windows Hello 傳回了未知的結果（%d）", En: "Windows Hello returned an unknown result (%d)", Ja: "Windows Hello から不明な結果が返されました（%d）", Ko: "Windows Hello가 알 수 없는 결과를 반환했습니다(%d)"},

		// webproxy
		"这台电脑的系统版本无法让网页内容走代理，缩略图和头像会继续直连；需要 macOS 14 或更高版本。": {Hant: "這台電腦的系統版本無法讓網頁內容透過代理連線，縮圖和頭像會繼續直接連線；需要 macOS 14 或更新版本。", En: "This computer's system version can't send web content through the proxy, so thumbnails and avatars keep connecting directly. macOS 14 or later is required.", Ja: "このコンピュータのシステムバージョンではウェブコンテンツをプロキシ経由にできないため、サムネイルとアバターは引き続き直接接続します。macOS 14 以降が必要です。", Ko: "이 컴퓨터의 시스템 버전에서는 웹 콘텐츠가 프록시를 거치게 할 수 없어 썸네일과 프로필 사진은 계속 직접 연결됩니다. macOS 14 이상이 필요합니다."},
		"这个版本暂时无法让网页内容走这里的代理，缩略图和头像按系统的网络设置连接。":              {Hant: "此版本暫時無法讓網頁內容透過這裡的代理連線，縮圖和頭像會依系統的網路設定連線。", En: "This version can't send web content through this proxy yet; thumbnails and avatars connect according to the system's network settings.", Ja: "このバージョンではまだウェブコンテンツをこのプロキシ経由にできません。サムネイルとアバターはシステムのネットワーク設定に従って接続します。", Ko: "이 버전에서는 아직 웹 콘텐츠가 이 프록시를 거치게 할 수 없습니다. 썸네일과 프로필 사진은 시스템 네트워크 설정에 따라 연결됩니다."},

		// drive
		"还没有登录账号":     {Hant: "尚未登入帳號", En: "No account signed in yet", Ja: "まだアカウントにログインしていません", Ko: "아직 로그인한 계정이 없습니다"},
		"登录已过期，请重新登录": {Hant: "登入已過期，請重新登入", En: "Your sign-in has expired. Sign in again", Ja: "ログインの有効期限が切れました。もう一度ログインしてください", Ko: "로그인이 만료되었습니다. 다시 로그인하세요"},

		// download
		"云端文件已不存在":            {Hant: "雲端檔案已不存在", En: "The file no longer exists in the drive", Ja: "クラウドのファイルはもう存在しません", Ko: "클라우드 파일이 더 이상 존재하지 않습니다"},
		"云端文件与开始下载时不一致，请重新下载": {Hant: "雲端檔案與開始下載時不一致，請重新下載", En: "The file in the drive changed since the download started. Download it again", Ja: "クラウドのファイルがダウンロード開始時から変更されました。もう一度ダウンロードしてください", Ko: "클라우드 파일이 다운로드를 시작했을 때와 다릅니다. 다시 다운로드하세요"},
		"服务器返回的数据范围与请求不符":     {Hant: "伺服器傳回的資料範圍與請求不符", En: "The server returned a different data range than requested", Ja: "サーバーが返したデータ範囲がリクエストと一致しません", Ko: "서버가 반환한 데이터 범위가 요청과 다릅니다"},
		"磁盘空间不足":              {Hant: "磁碟空間不足", En: "Not enough disk space", Ja: "ディスクの空き容量が不足しています", Ko: "디스크 공간이 부족합니다"},
		"这个账号同时下载的连接已满（PikPak 限制），请稍后点「继续」": {Hant: "此帳號的同時下載連線已滿（PikPak 限制），請稍後點「繼續」", En: "This account has reached its limit of simultaneous download connections (a PikPak limit). Click \"Resume\" later", Ja: "このアカウントの同時ダウンロード接続数が上限に達しました（PikPak の制限）。しばらくしてから「再開」をクリックしてください", Ko: "이 계정의 동시 다운로드 연결이 한도에 도달했습니다(PikPak 제한). 잠시 후 ‘계속’을 클릭하세요"},
		"连接 30 秒没有收到数据":             {Hant: "連線 30 秒未收到資料", En: "No data received for 30 seconds", Ja: "30 秒間データを受信していません", Ko: "30초 동안 데이터를 받지 못했습니다"},
		"服务器提前断开了连接":                {Hant: "伺服器提前中斷了連線", En: "The server closed the connection early", Ja: "サーバーが接続を途中で切断しました", Ko: "서버가 연결을 일찍 끊었습니다"},
		"云端文件处于归档存储，暂时取不出数据":        {Hant: "雲端檔案位於封存儲存，暫時無法取出資料", En: "The file is in archive storage and can't be read right now", Ja: "クラウドのファイルはアーカイブストレージにあるため、現在データを取り出せません", Ko: "클라우드 파일이 보관 저장소에 있어 지금은 데이터를 가져올 수 없습니다"},
		"下载链接被拒绝（HTTP %d）":          {Hant: "下載連結遭拒（HTTP %d）", En: "Download link refused (HTTP %d)", Ja: "ダウンロードリンクが拒否されました（HTTP %d）", Ko: "다운로드 링크가 거부되었습니다(HTTP %d)"},
		"服务器繁忙":                     {Hant: "伺服器忙碌中", En: "Server busy", Ja: "サーバーが混み合っています", Ko: "서버가 혼잡합니다"},
		"无法创建文件夹：%v":                {Hant: "無法建立資料夾：%v", En: "Can't create the folder: %v", Ja: "フォルダを作成できません：%v", Ko: "폴더를 만들 수 없습니다: %v"},
		"磁盘空间不足：还需 %s，可用 %s":        {Hant: "磁碟空間不足：還需要 %s，可用 %s", En: "Not enough disk space: %s needed, %s available", Ja: "ディスクの空き容量が不足しています：あと %s 必要で、空きは %s です", Ko: "디스크 공간이 부족합니다: %s 필요, %s 사용 가능"},
		"写入本地文件失败：%v":               {Hant: "寫入本機檔案失敗：%v", En: "Couldn't write the local file: %v", Ja: "ローカルファイルへの書き込みに失敗しました：%v", Ko: "로컬 파일에 쓰지 못했습니다: %v"},
		"下载没有完成":                    {Hant: "下載未完成", En: "The download didn't finish", Ja: "ダウンロードが完了しませんでした", Ko: "다운로드가 완료되지 않았습니다"},
		"无法创建本地文件：%v":               {Hant: "無法建立本機檔案：%v", En: "Can't create the local file: %v", Ja: "ローカルファイルを作成できません：%v", Ko: "로컬 파일을 만들 수 없습니다: %v"},
		"无法保存下载的文件：%v":              {Hant: "無法儲存下載的檔案：%v", En: "Can't save the downloaded file: %v", Ja: "ダウンロードしたファイルを保存できません：%v", Ko: "다운로드한 파일을 저장할 수 없습니다: %v"},
		"云端文件已在回收站里":                {Hant: "雲端檔案已在垃圾桶中", En: "The file is in the Trash", Ja: "クラウドのファイルはゴミ箱にあります", Ko: "클라우드 파일이 휴지통에 있습니다"},
		"云端还没有可下载的链接，文件可能尚未保存完成":    {Hant: "雲端尚無可下載的連結，檔案可能還沒儲存完成", En: "The drive has no download link yet; the file may not be fully saved", Ja: "クラウドにまだダウンロードリンクがありません。ファイルの保存が完了していない可能性があります", Ko: "클라우드에 아직 다운로드 링크가 없습니다. 파일 저장이 완료되지 않았을 수 있습니다"},
		"网络连接反复中断：%v":               {Hant: "網路連線反覆中斷：%v", En: "The network connection kept dropping: %v", Ja: "ネットワーク接続が何度も切断されました：%v", Ko: "네트워크 연결이 계속 끊어졌습니다: %v"},
		"服务器没有按分段返回数据":              {Hant: "伺服器未依分段傳回資料", En: "The server didn't return the data in ranges", Ja: "サーバーが分割してデータを返しませんでした", Ko: "서버가 데이터를 구간별로 반환하지 않았습니다"},
		"服务器暂时出错（HTTP %d）":          {Hant: "伺服器暫時發生錯誤（HTTP %d）", En: "Temporary server error (HTTP %d)", Ja: "サーバーで一時的なエラーが発生しました（HTTP %d）", Ko: "일시적인 서버 오류입니다(HTTP %d)"},
		"下载失败（HTTP %d）":             {Hant: "下載失敗（HTTP %d）", En: "Download failed (HTTP %d)", Ja: "ダウンロードに失敗しました（HTTP %d）", Ko: "다운로드에 실패했습니다(HTTP %d)"},
		"下载链接被拒绝（HTTP %d），请稍后点「继续」": {Hant: "下載連結遭拒（HTTP %d），請稍後點「繼續」", En: "Download link refused (HTTP %d). Click \"Resume\" later", Ja: "ダウンロードリンクが拒否されました（HTTP %d）。しばらくしてから「再開」をクリックしてください", Ko: "다운로드 링크가 거부되었습니다(HTTP %d). 잠시 후 ‘계속’을 클릭하세요"},
		"「%s」的同名文件夹太多":              {Hant: "與「%s」同名的資料夾太多", En: "Too many folders named \"%s\"", Ja: "「%s」と同じ名前のフォルダが多すぎます", Ko: "‘%s’과(와) 같은 이름의 폴더가 너무 많습니다"},
		"「%s」的同名文件太多":               {Hant: "與「%s」同名的檔案太多", En: "Too many files named \"%s\"", Ja: "「%s」と同じ名前のファイルが多すぎます", Ko: "‘%s’과(와) 같은 이름의 파일이 너무 많습니다"},
		"下载列表里没有这一项":                {Hant: "下載清單中沒有這一項", En: "This item isn't in the download list", Ja: "この項目はダウンロードリストにありません", Ko: "다운로드 목록에 이 항목이 없습니다"},
		"这个文件已经在下载列表里":              {Hant: "此檔案已在下載清單中", En: "This file is already in the download list", Ja: "このファイルはすでにダウンロードリストにあります", Ko: "이 파일은 이미 다운로드 목록에 있습니다"},
		"没有指定要下载的文件":                {Hant: "未指定要下載的檔案", En: "No file to download was specified", Ja: "ダウンロードするファイルが指定されていません", Ko: "다운로드할 파일이 지정되지 않았습니다"},
		"保存位置必须是完整的文件夹路径":           {Hant: "儲存位置必須是完整的資料夾路徑", En: "The save location must be a full folder path", Ja: "保存先は完全なフォルダパスである必要があります", Ko: "저장 위치는 전체 폴더 경로여야 합니다"},
		"这一项还没有停止":                  {Hant: "這一項尚未停止", En: "This item hasn't stopped yet", Ja: "この項目はまだ停止していません", Ko: "이 항목은 아직 멈추지 않았습니다"},

		// upload
		"「%s」不是普通文件，不能上传": {Hant: "「%s」不是一般檔案，無法上傳", En: "\"%s\" isn't a regular file and can't be uploaded", Ja: "「%s」は通常のファイルではないため、アップロードできません", Ko: "‘%s’은(는) 일반 파일이 아니어서 업로드할 수 없습니다"},
		"上传列表里没有这一项":      {Hant: "上傳清單中沒有這一項", En: "This item isn't in the upload list", Ja: "この項目はアップロードリストにありません", Ko: "업로드 목록에 이 항목이 없습니다"},
		"这一项还没有结束":        {Hant: "這一項尚未結束", En: "This item hasn't finished yet", Ja: "この項目はまだ終了していません", Ko: "이 항목은 아직 끝나지 않았습니다"},

		// ai
		"不支持的模型服务「%s」":                         {Hant: "不支援的模型服務「%s」", En: "Unsupported model service \"%s\"", Ja: "サポートされていないモデルサービス「%s」", Ko: "지원하지 않는 모델 서비스 ‘%s’"},
		"还没有填写服务地址":                            {Hant: "尚未填寫服務位址", En: "No service URL entered yet", Ja: "サービスの URL がまだ入力されていません", Ko: "아직 서비스 주소를 입력하지 않았습니다"},
		"输出名称「%s」不合要求":                         {Hant: "輸出名稱「%s」不符合要求", En: "Output name \"%s\" isn't valid", Ja: "出力名「%s」は要件を満たしていません", Ko: "출력 이름 ‘%s’이(가) 요구 사항에 맞지 않습니다"},
		"输出格式（JSON Schema）不是一个 JSON 对象":        {Hant: "輸出格式（JSON Schema）不是一個 JSON 物件", En: "The output format (JSON Schema) isn't a JSON object", Ja: "出力形式（JSON Schema）が JSON オブジェクトではありません", Ko: "출력 형식(JSON Schema)이 JSON 객체가 아닙니다"},
		"请求里没有消息":                              {Hant: "請求中沒有訊息", En: "The request has no messages", Ja: "リクエストにメッセージがありません", Ko: "요청에 메시지가 없습니다"},
		"消息角色「%s」不合要求":                         {Hant: "訊息角色「%s」不符合要求", En: "Message role \"%s\" isn't valid", Ja: "メッセージのロール「%s」は要件を満たしていません", Ko: "메시지 역할 ‘%s’이(가) 요구 사항에 맞지 않습니다"},
		"要求固定格式输出的请求不能带工具调用":                   {Hant: "要求固定格式輸出的請求不能包含工具呼叫", En: "A request for structured output can't include tool calls", Ja: "固定形式の出力を求めるリクエストにはツール呼び出しを含められません", Ko: "고정 형식 출력을 요구하는 요청에는 도구 호출을 포함할 수 없습니다"},
		"服务地址无效：%v":                            {Hant: "服務位址無效：%v", En: "Invalid service URL: %v", Ja: "サービスの URL が無効です：%v", Ko: "서비스 주소가 잘못되었습니다: %v"},
		"等待模型服务超时：%v":                          {Hant: "等待模型服務逾時：%v", En: "Timed out waiting for the model service: %v", Ja: "モデルサービスの応答待ちがタイムアウトしました：%v", Ko: "모델 서비스 응답 대기 시간이 초과되었습니다: %v"},
		"服务返回的不是 JSON：%s":                      {Hant: "服務傳回的不是 JSON：%s", En: "The service didn't return JSON: %s", Ja: "サービスが JSON 以外を返しました：%s", Ko: "서비스가 JSON이 아닌 응답을 반환했습니다: %s"},
		"回答达到输出上限":                             {Hant: "回答達到輸出上限", En: "The answer hit the output limit", Ja: "回答が出力の上限に達しました", Ko: "답변이 출력 한도에 도달했습니다"},
		"工具调用的输入不是 JSON 对象：%s":                 {Hant: "工具呼叫的輸入不是 JSON 物件：%s", En: "The tool call's input isn't a JSON object: %s", Ja: "ツール呼び出しの入力が JSON オブジェクトではありません：%s", Ko: "도구 호출의 입력이 JSON 객체가 아닙니다: %s"},
		"回答里没有工具调用：%s":                         {Hant: "回答中沒有工具呼叫：%s", En: "The answer has no tool call: %s", Ja: "回答にツール呼び出しがありません：%s", Ko: "답변에 도구 호출이 없습니다: %s"},
		"（空）":                                  {Hant: "（空）", En: "(empty)", Ja: "（空）", Ko: "(비어 있음)"},
		"这个地址的回答不像 Anthropic 模型服务：%s":          {Hant: "此位址的回應不像 Anthropic 模型服務：%s", En: "The answer from this URL doesn't look like an Anthropic model service: %s", Ja: "この URL の応答は Anthropic のモデルサービスのものではないようです：%s", Ko: "이 주소의 응답은 Anthropic 모델 서비스로 보이지 않습니다: %s"},
		"工具调用「%s」不合要求":                         {Hant: "工具呼叫「%s」不符合要求", En: "Tool call \"%s\" isn't valid", Ja: "ツール呼び出し「%s」は要件を満たしていません", Ko: "도구 호출 ‘%s’이(가) 요구 사항에 맞지 않습니다"},
		"工具结果没有对应的调用":                          {Hant: "工具結果沒有對應的呼叫", En: "A tool result has no matching call", Ja: "ツールの結果に対応する呼び出しがありません", Ko: "도구 결과에 대응하는 호출이 없습니다"},
		"工具名称「%s」不合要求":                         {Hant: "工具名稱「%s」不符合要求", En: "Tool name \"%s\" isn't valid", Ja: "ツール名「%s」は要件を満たしていません", Ko: "도구 이름 ‘%s’이(가) 요구 사항에 맞지 않습니다"},
		"工具 %s 的参数格式（JSON Schema）不是一个 JSON 对象": {Hant: "工具 %s 的參數格式（JSON Schema）不是一個 JSON 物件", En: "The parameter format (JSON Schema) of tool %s isn't a JSON object", Ja: "ツール %s のパラメータ形式（JSON Schema）が JSON オブジェクトではありません", Ko: "도구 %s의 매개변수 형식(JSON Schema)이 JSON 객체가 아닙니다"},
		"模型服务的回答中途断开":                          {Hant: "模型服務的回答中途中斷", En: "The model service's answer was cut off", Ja: "モデルサービスの回答が途中で切断されました", Ko: "모델 서비스의 답변이 도중에 끊어졌습니다"},
		"回答里没有内容：%s":                           {Hant: "回答中沒有內容：%s", En: "The answer has no content: %s", Ja: "回答に内容がありません：%s", Ko: "답변에 내용이 없습니다: %s"},
		"回答里没有 JSON 对象：%s":                     {Hant: "回答中沒有 JSON 物件：%s", En: "The answer has no JSON object: %s", Ja: "回答に JSON オブジェクトがありません：%s", Ko: "답변에 JSON 객체가 없습니다: %s"},
		"这个地址的回答不像 OpenAI 兼容的模型服务：%s":          {Hant: "此位址的回應不像 OpenAI 相容的模型服務：%s", En: "The answer from this URL doesn't look like an OpenAI-compatible model service: %s", Ja: "この URL の応答は OpenAI 互換のモデルサービスのものではないようです：%s", Ko: "이 주소의 응답은 OpenAI 호환 모델 서비스로 보이지 않습니다: %s"},

		// tmdb
		"请求超时":                  {Hant: "請求逾時", En: "Request timed out", Ja: "リクエストがタイムアウトしました", Ko: "요청 시간이 초과되었습니다"},
		"请求已取消":                 {Hant: "請求已取消", En: "Request cancelled", Ja: "リクエストはキャンセルされました", Ko: "요청이 취소되었습니다"},
		"连不上 %s：%s":             {Hant: "無法連線到 %s：%s", En: "Can't reach %s: %s", Ja: "%s に接続できません：%s", Ko: "%s에 연결할 수 없습니다: %s"},
		"还没有配置 TMDB 凭据":         {Hant: "尚未設定 TMDB 憑證", En: "No TMDB credentials set up yet", Ja: "TMDB の認証情報がまだ設定されていません", Ko: "아직 TMDB 자격 증명을 설정하지 않았습니다"},
		"TMDB 请求过于频繁，请 %d 秒后重试": {Hant: "TMDB 請求過於頻繁，請於 %d 秒後重試", En: "Too many TMDB requests. Try again in %d s", Ja: "TMDB へのリクエストが多すぎます。%d 秒後にもう一度お試しください", Ko: "TMDB 요청이 너무 많습니다. %d초 후 다시 시도하세요"},
		"TMDB 没有这个条目":           {Hant: "TMDB 沒有這個項目", En: "TMDB has no such entry", Ja: "TMDB にこの項目はありません", Ko: "TMDB에 이 항목이 없습니다"},
		"TMDB 凭据无效或已停用":         {Hant: "TMDB 憑證無效或已停用", En: "The TMDB credentials are invalid or revoked", Ja: "TMDB の認証情報が無効か、停止されています", Ko: "TMDB 자격 증명이 잘못되었거나 사용 중지되었습니다"},
		"TMDB 返回错误（HTTP %d）":    {Hant: "TMDB 傳回錯誤（HTTP %d）", En: "TMDB returned an error (HTTP %d)", Ja: "TMDB がエラーを返しました（HTTP %d）", Ko: "TMDB가 오류를 반환했습니다(HTTP %d)"},
		"TMDB 返回了无法识别的数据":       {Hant: "TMDB 傳回了無法辨識的資料", En: "TMDB returned data PKP can't read", Ja: "TMDB が認識できないデータを返しました", Ko: "TMDB가 인식할 수 없는 데이터를 반환했습니다"},
		"TMDB 地址无效":             {Hant: "TMDB 位址無效", En: "Invalid TMDB URL", Ja: "TMDB の URL が無効です", Ko: "TMDB 주소가 잘못되었습니다"},
		"TMDB 地址跳转到了其他站点，已拒绝":   {Hant: "TMDB 位址重新導向到其他網站，已拒絕", En: "The TMDB URL redirected to another site; refused", Ja: "TMDB の URL が別のサイトにリダイレクトされたため、拒否しました", Ko: "TMDB 주소가 다른 사이트로 리디렉션되어 거부했습니다"},
		"作品类型无效":                {Hant: "作品類型無效", En: "Invalid title type", Ja: "作品の種類が無効です", Ko: "작품 유형이 잘못되었습니다"},
		"请输入要搜索的片名":             {Hant: "請輸入要搜尋的片名", En: "Enter a title to search for", Ja: "検索する作品名を入力してください", Ko: "검색할 작품명을 입력하세요"},
		"搜索词太长（最多 %d 个字）":       {Hant: "搜尋詞太長（最多 %d 個字）", En: "The search is too long (at most %d characters)", Ja: "検索語が長すぎます（最大 %d 文字）", Ko: "검색어가 너무 깁니다(최대 %d자)"},
	})
}
