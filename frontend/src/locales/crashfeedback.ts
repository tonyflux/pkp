import type { Messages } from "./types";

const messages: Messages = {
  "崩溃反馈": { tw: "當機回饋", en: "Crash feedback", ja: "クラッシュのフィードバック", ko: "충돌 피드백" },
  "崩溃报告已发送": { tw: "當機報告已傳送", en: "Crash report sent", ja: "クラッシュレポートを送信しました", ko: "충돌 보고서를 보냈습니다" },
  "上次运行时发生了崩溃": { tw: "上次執行時發生了當機", en: "PKP crashed during its last run", ja: "前回の実行中にクラッシュしました", ko: "지난 실행 중 충돌이 발생했습니다" },
  "未经您的明确同意不会上传。点击发送后，报告将由 Cloudflare 处理并发送给开发者。": { tw: "未經您明確同意不會上傳。點擊傳送後，報告將由 Cloudflare 處理並傳送給開發者。", en: "Nothing is uploaded without your explicit consent. If you click Send, Cloudflare will process the report for delivery to the developer.", ja: "明示的な同意なしにアップロードされることはありません。送信をクリックすると、Cloudflare がレポートを処理し、開発者に送信します。", ko: "명시적으로 동의하기 전에는 업로드하지 않습니다. 보내기를 누르면 Cloudflare가 보고서를 처리하여 개발자에게 전달합니다." },
  "报告可能包含路径、账号信息或其他敏感内容。请先审阅并删除不想分享的信息；下方内容可以修改，发送的正是修改后的文本。": { tw: "報告可能包含路徑、帳號資訊或其他敏感內容。請先審閱並刪除不想分享的資訊；下方內容可以修改，傳送的正是修改後的文字。", en: "The report may contain paths, account details, or other sensitive information. Review it and remove anything you do not want to share. You can edit the text below; exactly that edited text will be sent.", ja: "レポートにはパス、アカウント情報などの機密情報が含まれる場合があります。確認し、共有したくない情報を削除してください。下のテキストを編集でき、編集後の内容がそのまま送信されます。", ko: "보고서에는 경로, 계정 정보 또는 기타 민감한 내용이 포함될 수 있습니다. 검토하고 공유하지 않을 정보를 삭제하세요. 아래 내용을 수정할 수 있으며 수정한 텍스트가 그대로 전송됩니다." },
  "崩溃反馈与匿名使用统计无关，即使关闭统计也可主动发送。": { tw: "當機回饋與匿名使用統計無關，即使關閉統計也可主動傳送。", en: "Crash feedback is separate from anonymous usage statistics; you can send it even if statistics are off.", ja: "クラッシュのフィードバックは匿名の使用統計とは別です。統計を無効にしていても送信できます。", ko: "충돌 피드백은 익명 사용 통계와 별개이며 통계를 꺼도 직접 보낼 수 있습니다." },
  "开发者可查看并删除报告；服务端计划每日清理超过 29 天的报告，发送后无法自动撤回。": { tw: "開發者可查看並刪除報告；伺服器預定每日清除超過 29 天的報告，傳送後無法自動撤回。", en: "The developer can view and delete reports. The server is scheduled to remove reports older than 29 days each day; sending cannot be automatically undone.", ja: "開発者はレポートを閲覧・削除できます。サーバーは29日を超えたレポートを毎日削除する予定です。送信後に自動で取り消すことはできません。", ko: "개발자는 보고서를 보고 삭제할 수 있습니다. 서버는 29일이 지난 보고서를 매일 삭제하도록 예약되어 있으며, 보낸 후 자동으로 철회할 수 없습니다." },
  "原始日志过长，此预览已截取；本地原始日志不受影响。": { tw: "原始日誌過長，此預覽已截取；本機原始日誌不受影響。", en: "The original log was too long, so this preview is truncated. The original local log is unchanged.", ja: "元のログが長すぎるため、プレビューは一部のみです。ローカルの元のログは変更されません。", ko: "원본 로그가 너무 길어 미리 보기가 잘렸습니다. 로컬 원본 로그는 변경되지 않습니다." },
  "要发送的报告": { tw: "要傳送的報告", en: "Report to send", ja: "送信するレポート", ko: "보낼 보고서" },
  "报告或编码后的请求过大，请删减后再发送（报告最多 120 KiB，请求最多 128 KiB）。": { tw: "報告或編碼後的請求過大，請刪減後再傳送（報告最多 120 KiB，請求最多 128 KiB）。", en: "The report or encoded request is too large. Remove some text before sending (report: 120 KiB maximum; request: 128 KiB maximum).", ja: "レポートまたはエンコード後のリクエストが大きすぎます。削除してから送信してください（レポートは最大 120 KiB、リクエストは最大 128 KiB）。", ko: "보고서 또는 인코딩된 요청이 너무 큽니다. 내용을 줄인 후 보내세요 (보고서 최대 120 KiB, 요청 최대 128 KiB)." },
  "崩溃报告操作失败，请重试。": { tw: "當機報告操作失敗，請重試。", en: "Crash report action failed. Please try again.", ja: "クラッシュレポートの操作に失敗しました。再試行してください。", ko: "충돌 보고서 작업에 실패했습니다. 다시 시도하세요." },
  "不发送或关闭将保留本地原始日志，并且不再提示此报告。": { tw: "不傳送或關閉將保留本機原始日誌，並且不再提示此報告。", en: "Choosing Don't send or closing keeps the original log locally and stops reminders for this report.", ja: "送信しないか閉じると、元のログはローカルに保持され、このレポートの通知は今後表示されません。", ko: "보내지 않거나 닫으면 원본 로그는 로컬에 유지되며 이 보고서는 다시 알리지 않습니다." },
  "发送报告": { tw: "傳送報告", en: "Send report", ja: "レポートを送信", ko: "보고서 보내기" },
};
export default messages;
