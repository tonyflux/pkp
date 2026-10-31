import type { Messages } from "./types";

export default {
  "office|文档包含不安全的 XML": { tw: "Office 文件包含不安全的 XML", en: "The Office document contains unsafe XML", ja: "Office 文書に安全でない XML が含まれています", ko: "Office 문서에 안전하지 않은 XML이 포함되어 있습니다" },
  "office|文档的 XML 已损坏": { tw: "Office 文件的 XML 已損毀", en: "The Office document contains invalid XML", ja: "Office 文書の XML が破損しています", ko: "Office 문서의 XML이 손상되었습니다" },
  "office|文档超过安全预览上限": { tw: "Office 文件超過安全預覽上限", en: "The Office document exceeds the safe preview limits", ja: "Office 文書が安全なプレビュー上限を超えています", ko: "Office 문서가 안전한 미리보기 제한을 초과했습니다" },
  "office|文档结构不完整": { tw: "Office 文件結構不完整", en: "The Office document structure is incomplete", ja: "Office 文書の構造が不完全です", ko: "Office 문서 구조가 불완전합니다" },
  "office|缩略图": { tw: "縮圖", en: "Thumbnails", ja: "サムネイル", ko: "썸네일" },
  "office|上一页": { tw: "上一頁", en: "Previous page", ja: "前のページ", ko: "이전 페이지" },
  "office|下一页": { tw: "下一頁", en: "Next page", ja: "次のページ", ko: "다음 페이지" },
  "office|页码": { tw: "頁碼", en: "Page number", ja: "ページ番号", ko: "페이지 번호" },
  "office|放大": { tw: "放大", en: "Zoom in", ja: "拡大", ko: "확대" },
  "office|缩小": { tw: "縮小", en: "Zoom out", ja: "縮小", ko: "축소" },
  "office|适合宽度": { tw: "符合寬度", en: "Fit width", ja: "幅に合わせる", ko: "너비에 맞춤" },
  "office|搜索文档": { tw: "搜尋文件", en: "Search document", ja: "文書内を検索", ko: "문서 검색" },
  "office|查找下一处": { tw: "尋找下一處", en: "Find next", ja: "次を検索", ko: "다음 찾기" },
  "office|已找到匹配内容": { tw: "已跳至符合內容的頁面", en: "Moved to a matching page", ja: "一致するページに移動しました", ko: "일치하는 페이지로 이동했습니다" },
  "office|未找到匹配内容": { tw: "找不到符合的內容", en: "No matching text found", ja: "一致するテキストが見つかりません", ko: "일치하는 텍스트를 찾을 수 없습니다" },
  "office|正在安全解析文档": { tw: "正在安全解析文件…", en: "Safely parsing document…", ja: "文書を安全に解析中…", ko: "문서를 안전하게 분석 중…" },
  "office|跳到第{page}页": { tw: "前往第 {page} 頁", en: "Go to page {page}", ja: "{page} ページに移動", ko: "{page}페이지로 이동" },
  "只读本地预览，不连接外部网络。使用本机字体，分页与复杂版式可能不同。不显示图表、动画、影音、宏、嵌入对象和外部内容。上限：压缩 32 MB／展开 96 MB，DOCX 300 页；需整份排版。": {
    tw: "唯讀本機預覽，無外部連線。使用本機字型；分頁及複雜版面可能不同。圖表、動畫、影音、巨集、嵌入物件及外部內容不顯示。上限：32 MB 壓縮／96 MB 展開、DOCX 300 頁；需整份排版。",
    en: "Read-only local preview; no external connections. Local fonts are used; pagination and complex layouts may differ. Charts, animation, audio/video, macros, embedded objects and external content are omitted. Limits: 32 MB compressed / 96 MB expanded, 300 DOCX pages; whole-document layout is required.",
    ja: "読み取り専用のローカル表示。外部通信は行いません。端末のフォントを使用し、改ページや複雑なレイアウトは異なる場合があります。グラフ、アニメーション、音声・動画、マクロ、埋め込みオブジェクト、外部コンテンツは表示しません。上限：圧縮 32 MB／展開 96 MB、DOCX 300 ページ。全文をレイアウトします。",
    ko: "읽기 전용 로컬 미리보기이며 외부 연결을 하지 않습니다. 로컬 글꼴을 사용하므로 페이지 나누기와 복잡한 레이아웃이 다를 수 있습니다. 차트, 애니메이션, 오디오·비디오, 매크로, 포함된 개체 및 외부 콘텐츠는 표시하지 않습니다. 제한: 압축 32 MB / 압축 해제 96 MB, DOCX 300페이지. 문서 전체를 레이아웃합니다.",
  },
} satisfies Messages;
