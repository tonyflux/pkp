import type { Messages } from "./types";
export default {
  "读取文件失败（HTTP {status}）": { tw: "讀取檔案失敗（HTTP {status}）", en: "Could not read file (HTTP {status})", ja: "ファイルを読み込めません（HTTP {status}）", ko: "파일을 읽지 못했습니다(HTTP {status})" },
  "内容超过安全读取上限": { tw: "內容超過安全讀取上限", en: "Content exceeds the safe reading limit", ja: "コンテンツが安全な読み込み上限を超えています", ko: "콘텐츠가 안전한 읽기 한도를 초과합니다" },
  "文件服务器没有返回有效的字节范围": { tw: "檔案伺服器未傳回有效的位元組範圍", en: "The file server returned an invalid byte range", ja: "ファイルサーバーのバイト範囲が無効です", ko: "파일 서버가 잘못된 바이트 범위를 반환했습니다" },
  "文件内容不完整": { tw: "檔案內容不完整", en: "File content is incomplete", ja: "ファイルの内容が不完全です", ko: "파일 내용이 불완전합니다" },
  "压缩容器中的项目过多": { tw: "壓縮容器中的項目過多", en: "The archive contains too many entries", ja: "アーカイブ内の項目数が多すぎます", ko: "압축 파일의 항목이 너무 많습니다" },
  "压缩容器包含不安全的路径": { tw: "壓縮容器包含不安全的路徑", en: "The archive contains an unsafe path", ja: "アーカイブに安全でないパスが含まれています", ko: "압축 파일에 안전하지 않은 경로가 있습니다" },
  "压缩容器超过安全展开上限": { tw: "壓縮容器超過安全展開上限", en: "The archive exceeds the safe expansion limit", ja: "アーカイブが安全な展開上限を超えています", ko: "압축 파일이 안전한 압축 해제 한도를 초과합니다" },
  "不支持加密的压缩容器": { tw: "不支援加密的壓縮容器", en: "Encrypted archives are not supported", ja: "暗号化されたアーカイブには対応していません", ko: "암호화된 압축 파일은 지원하지 않습니다" },
  "文档资源不存在：{path}": { tw: "文件資源不存在：{path}", en: "Document resource not found: {path}", ja: "文書のリソースがありません：{path}", ko: "문서 리소스를 찾을 수 없습니다: {path}" },
  "此图片格式不能安全预览": { tw: "此圖片格式無法安全預覽", en: "This image format cannot be previewed safely", ja: "この画像形式は安全にプレビューできません", ko: "이 이미지 형식은 안전하게 미리 볼 수 없습니다" },
  "作为漫画阅读": { tw: "以漫畫方式閱讀", en: "Read as comic", ja: "コミックとして読む", ko: "만화로 읽기" },
  "漫画": { tw: "漫畫", en: "Comic", ja: "コミック", ko: "만화" },
  "表格": { tw: "表格", en: "Table", ja: "表", ko: "표" },
  "字幕与歌词": { tw: "字幕與歌詞", en: "Subtitles & lyrics", ja: "字幕と歌詞", ko: "자막 및 가사" },
  "文档结构过于复杂，无法安全预览": { tw: "文件結構過於複雜，無法安全預覽", en: "Document structure is too complex to preview safely", ja: "文書構造が複雑すぎるため安全にプレビューできません", ko: "문서 구조가 너무 복잡하여 안전하게 미리 볼 수 없습니다" },
} satisfies Messages;
