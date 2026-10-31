import type { Messages } from "./types";

export default {
  "timed|文本编码": { tw: "文字編碼", en: "Text encoding", ja: "文字コード", ko: "텍스트 인코딩" },
  "timed|自动（UTF-8 / GB18030）": { tw: "自動（UTF-8 / GB18030）", en: "Auto (UTF-8 / GB18030)", ja: "自動（UTF-8 / GB18030）", ko: "자동 (UTF-8 / GB18030)" },
  "timed|搜索字幕文本": { tw: "搜尋字幕文字", en: "Search cue text", ja: "字幕テキストを検索", ko: "자막 텍스트 검색" },
  "timed|定位时间": { tw: "定位時間", en: "Go to time", ja: "時刻へ移動", ko: "시간으로 이동" },
  "timed|时:分:秒.毫秒": { tw: "時:分:秒.毫秒", en: "hh:mm:ss.mmm", ja: "時:分:秒.ミリ秒", ko: "시:분:초.밀리초" },
  "timed|{n} 条": { tw: "{n} 條", en: "{n} cues", ja: "{n} 件", ko: "{n}개 항목" },
  "timed|只读时间轴，不控制播放器": { tw: "唯讀時間軸，不控制播放器", en: "Read-only timeline; does not control playback", ja: "読み取り専用のタイムラインです。プレーヤーは操作しません", ko: "읽기 전용 타임라인이며 플레이어를 제어하지 않습니다" },
  "timed|没有匹配的字幕": { tw: "沒有符合的字幕", en: "No matching cues", ja: "一致する字幕がありません", ko: "일치하는 자막이 없습니다" },
  "timed|未找到有效的时间条目": { tw: "未找到有效的時間項目", en: "No valid timed cues found", ja: "有効なタイムスタンプ付きの項目が見つかりません", ko: "유효한 시간 항목을 찾을 수 없습니다" },
  "timed|字幕条目超过安全上限": { tw: "字幕項目超過安全上限", en: "The cue count exceeds the safety limit", ja: "字幕の件数が安全上限を超えています", ko: "자막 항목 수가 안전 한도를 초과했습니다" },
} satisfies Messages;
