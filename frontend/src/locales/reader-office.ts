import type { Messages } from "./types";

export default {
  "office|文档包含不支持的格式或外部引用": { tw: "Office 文件包含不支援的格式或外部引用", en: "The Office document contains unsupported XML", ja: "Office 文書に非対応の形式が含まれています", ko: "Office 문서에 지원하지 않는 형식이 포함되어 있습니다" },
  "office|文档的 XML 已损坏": { tw: "Office 文件的 XML 已損毀", en: "The Office document contains invalid XML", ja: "Office 文書の XML が破損しています", ko: "Office 문서의 XML이 손상되었습니다" },
  "office|文档太大或太长，超出了预览上限": { tw: "Office 文件太大或太長，超出了預覽上限", en: "The Office document is too large or long to preview", ja: "Office 文書が大きすぎるか長すぎるためプレビューできません", ko: "Office 문서가 너무 크거나 길어 미리 볼 수 없습니다" },
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
  "office|正在打开文档…": { tw: "正在開啟文件…", en: "Opening document…", ja: "文書を開いています…", ko: "문서를 여는 중…" },
  "office|跳到第{page}页": { tw: "前往第 {page} 頁", en: "Go to page {page}", ja: "{page} ページに移動", ko: "{page}페이지로 이동" },
  "只在本地只读预览，不联网，用的是本机字体，分页和复杂排版可能和原文件略有差别；图表、动画、音视频、宏和外部内容不显示（支持压缩 32 MB／展开 96 MB、DOCX 300 页以内）。": {
    tw: "只在本機唯讀預覽，不連網，用的是本機字型，分頁和複雜排版可能和原檔案略有差異；圖表、動畫、影音、巨集與外部內容不顯示（支援壓縮 32 MB／展開 96 MB、DOCX 300 頁以內）。",
    en: "Read-only offline preview using local fonts, so pagination and complex layouts may differ slightly. Charts, animations, media, macros and external content are hidden (limit: 32 MB compressed / 96 MB expanded, up to 300 DOCX pages).",
    ja: "ローカルのみの読み取り専用プレビュー。通信せず端末のフォントを使うため、改ページや複雑な組版は少し異なる場合があります。グラフ、アニメーション、動画・音声、マクロ、外部コンテンツは非表示（上限：圧縮 32 MB／展開 96 MB、DOCX 300 ページ）。",
    ko: "오프라인 읽기 전용 미리보기입니다. 로컬 글꼴을 써서 페이지 나누기나 복잡한 배치가 조금 다를 수 있으며 차트·애니메이션·미디어·매크로·외부 콘텐츠는 숨깁니다(최대 압축 32 MB / 해제 96 MB, DOCX 300페이지).",
  },
} satisfies Messages;
