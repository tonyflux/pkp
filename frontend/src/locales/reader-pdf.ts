import type { Messages } from "./types";

export default {
  "pdf|目录": { tw: "目錄", en: "Outline", ja: "目次", ko: "목차" },
  "pdf|页码": { tw: "頁碼", en: "Page number", ja: "ページ番号", ko: "페이지 번호" },
  "pdf|前往": { tw: "前往", en: "Go", ja: "移動", ko: "이동" },
  "pdf|缩放": { tw: "縮放", en: "Zoom", ja: "ズーム", ko: "확대/축소" },
  "pdf|适合整页": { tw: "符合整頁", en: "Fit page", ja: "ページ全体", ko: "페이지에 맞춤" },
  "pdf|适合宽度": { tw: "符合寬度", en: "Fit width", ja: "幅に合わせる", ko: "너비에 맞춤" },
  "pdf|查找 PDF 文本": { tw: "尋找 PDF 文字", en: "Find PDF text", ja: "PDF のテキストを検索", ko: "PDF 텍스트 찾기" },
  "pdf|正在查找 {n}/{total}": { tw: "正在尋找 {n}/{total}", en: "Searching {n}/{total}", ja: "検索中 {n}/{total}", ko: "검색 중 {n}/{total}" },
  "pdf|匹配 {n}/{total}": { tw: "符合 {n}/{total}", en: "Match {n}/{total}", ja: "一致 {n}/{total}", ko: "일치 {n}/{total}" },
  "pdf|上一个匹配": { tw: "上一個符合項目", en: "Previous match", ja: "前の一致", ko: "이전 일치 항목" },
  "pdf|下一个匹配": { tw: "下一個符合項目", en: "Next match", ja: "次の一致", ko: "다음 일치 항목" },
  "pdf|密码不正确，请重试": { tw: "密碼不正確，請重試", en: "Incorrect password. Please try again.", ja: "パスワードが違います。再入力してください", ko: "비밀번호가 올바르지 않습니다. 다시 시도하세요" },
  "pdf|此 PDF 已加密，请输入打开密码": { tw: "此 PDF 已加密，請輸入開啟密碼", en: "This PDF is encrypted. Enter its opening password.", ja: "この PDF は暗号化されています。開くためのパスワードを入力してください", ko: "이 PDF는 암호화되어 있습니다. 열기 비밀번호를 입력하세요" },
  "pdf|PDF 打开密码": { tw: "PDF 開啟密碼", en: "PDF opening password", ja: "PDF を開くパスワード", ko: "PDF 열기 비밀번호" },
  "pdf|已取消 PDF 密码输入": { tw: "已取消 PDF 密碼輸入", en: "PDF password entry canceled", ja: "PDF パスワードの入力をキャンセルしました", ko: "PDF 비밀번호 입력이 취소되었습니다" },
  "pdf|此文档没有目录": { tw: "此文件沒有目錄", en: "This document has no outline", ja: "この文書には目次がありません", ko: "이 문서에는 목차가 없습니다" },
  "pdf|无法定位此目录项": { tw: "無法定位此目錄項目", en: "Could not locate this outline item", ja: "この目次項目の移動先が見つかりません", ko: "이 목차 항목의 위치를 찾을 수 없습니다" },
  "pdf|第 {n} 页": { tw: "第 {n} 頁", en: "Page {n}", ja: "{n} ページ", ko: "{n} 페이지" },
  "pdf|匹配过多，仅显示前十万项": { tw: "符合項目過多，僅顯示前十萬項", en: "Too many matches; showing the first 100,000", ja: "一致が多すぎるため、最初の 100,000 件を表示します", ko: "일치 항목이 너무 많아 처음 100,000개만 표시합니다" },
  "pdf|正在绘制页面…": { tw: "正在繪製頁面…", en: "Rendering page…", ja: "ページを描画中…", ko: "페이지를 렌더링하는 중…" },
} satisfies Messages;
