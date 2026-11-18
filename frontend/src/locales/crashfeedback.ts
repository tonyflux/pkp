import type { Messages } from "./types";

const messages: Messages = {
  "崩溃反馈": { tw: "當機回饋", en: "Crash feedback", ja: "クラッシュのフィードバック", ko: "충돌 피드백" },
  "崩溃报告已发送": { tw: "當機報告已傳送", en: "Crash report sent", ja: "クラッシュレポートを送信しました", ko: "충돌 보고서를 보냈습니다" },
  "上次运行时发生了崩溃": { tw: "上次執行時發生了當機", en: "PKP crashed during its last run", ja: "前回の実行中にクラッシュしました", ko: "지난 실행 중 충돌이 발생했습니다" },
  "放心，没点发送前什么都不会传；点击后报告会经由 Cloudflare 发给开发者。": { tw: "放心，沒點傳送前什麼都不會傳；點擊後報告會經由 Cloudflare 傳給開發者。", en: "Nothing is uploaded unless you send it. Clicking Send passes the report through Cloudflare to the developer.", ja: "「レポートを送信」を押すまで何も送信されません。送信すると Cloudflare を経由して開発者に届きます。", ko: "보내기를 누르기 전까지는 아무것도 업로드되지 않습니다. 보내면 Cloudflare를 거쳐 개발자에게 전달됩니다." },
  "报告里可能带有文件路径、账号之类的隐私信息；下面文本框可以直接删改，只会发出你改完后的内容。": { tw: "報告裡可能帶有檔案路徑、帳號之類的隱私資訊；下面文字框可以直接刪改，只會送出你改完後的內容。", en: "The report may include paths, account details, or other sensitive info. Edit the text below to remove anything private—only your edited version is sent.", ja: "レポートにはパスやアカウント情報などが含まれることがあります。下の欄から不要な部分を直接削除・編集でき、編集後の内容だけが送信されます。", ko: "보고서에 파일 경로, 계정 정보 등 민감한 내용이 포함될 수 있습니다. 아래 입력란에서 원하지 않는 내용을 직접 지울 수 있으며 수정한 내용만 전송됩니다." },
  "这是手动反馈通道，跟匿名统计无关，关掉统计后也一样能发。": { tw: "這是手動回饋管道，與匿名統計無關，關閉統計後也一樣能傳。", en: "Crash reports are separate from anonymous usage stats—you can still send one even if stats are turned off.", ja: "これは手動のフィードバックで、匿名の使用統計とは無関係です。統計がオフでも送信できます。", ko: "충돌 보고는 익명 사용 통계와 별개이므로 통계를 꺼 두어도 직접 보낼 수 있습니다." },
  "报告只给开发者排查问题用，服务端会在 29 天后自动清理；发出去后没法单方面撤回。": { tw: "報告僅供開發者排查問題使用，伺服器會在 29 天後自動清除；送出後無法單方面撤回。", en: "Reports are only for debugging and are scheduled to clear from the server after 29 days; once sent, they can't be recalled automatically.", ja: "レポートは開発者が不具合を確認するためだけに使用され、サーバー上では 29 日後に自動削除されます。送信後の自動取り消しはできません。", ko: "보고서는 개발자가 문제를 파악하는 데만 쓰이며 29일 후 서버에서 자동 삭제됩니다. 보낸 뒤 자동으로 회수할 수는 없습니다." },
  "原始日志太长，这里只截了一部分显示；你电脑上的完整日志不会变。": { tw: "原始日誌太長，這裡只截了一部分顯示；你電腦上的完整日誌不會變。", en: "The full log was too long, so this preview is shortened. The original log on your computer isn't touched.", ja: "元のログが長いため一部のみ表示しています。コンピュータ上の元のログはそのまま残ります。", ko: "원본 로그가 너무 길어 일부만 미리 보여 드립니다. 컴퓨터에 저장된 원본 로그는 그대로 유지됩니다." },
  "要发送的报告": { tw: "要傳送的報告", en: "Report to send", ja: "送信するレポート", ko: "보낼 보고서" },
  "报告或编码后的请求过大，请删减后再发送（报告最多 120 KiB，请求最多 128 KiB）。": { tw: "報告或編碼後的請求過大，請刪減後再傳送（報告最多 120 KiB，請求最多 128 KiB）。", en: "The report or encoded request is too large. Remove some text before sending (report: 120 KiB maximum; request: 128 KiB maximum).", ja: "レポートまたはエンコード後のリクエストが大きすぎます。削除してから送信してください（レポートは最大 120 KiB、リクエストは最大 128 KiB）。", ko: "보고서 또는 인코딩된 요청이 너무 큽니다. 내용을 줄인 후 보내세요 (보고서 최대 120 KiB, 요청 최대 128 KiB)." },
  "崩溃报告操作失败，请重试。": { tw: "當機報告操作失敗，請重試。", en: "Crash report action failed. Please try again.", ja: "クラッシュレポートの操作に失敗しました。再試行してください。", ko: "충돌 보고서 작업에 실패했습니다. 다시 시도하세요." },
  "不发或直接关闭不会删掉本地日志，下次也不会再弹这条提醒。": { tw: "不傳或直接關閉不會刪掉本機日誌，下次也不會再跳出這則提醒。", en: "Skipping or closing keeps the local log on your computer and won't remind you about this crash again.", ja: "送信せず閉じてもローカルのログは削除されず、このクラッシュについて再度通知されることもありません。", ko: "보내지 않고 닫아도 로컬 로그는 남으며 이 충돌에 대해서는 다시 알리지 않습니다." },
  "发送报告": { tw: "傳送報告", en: "Send report", ja: "レポートを送信", ko: "보고서 보내기" },
};
export default messages;
