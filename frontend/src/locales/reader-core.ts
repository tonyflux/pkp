import type { Messages } from "./types";
export default {
  "读取文件失败（HTTP {status}）": { tw: "讀取檔案失敗（HTTP {status}）", en: "Could not read file (HTTP {status})", ja: "ファイルを読み込めません（HTTP {status}）", ko: "파일을 읽지 못했습니다(HTTP {status})" },
  "文件太大，超出了预览限制": { tw: "檔案太大，超出了預覽限制", en: "File is too large to preview", ja: "ファイルが大きすぎてプレビューできません", ko: "파일이 너무 커서 미리 볼 수 없습니다" },
  "文件服务器没有返回有效的字节范围": { tw: "檔案伺服器未傳回有效的位元組範圍", en: "The file server returned an invalid byte range", ja: "ファイルサーバーのバイト範囲が無効です", ko: "파일 서버가 잘못된 바이트 범위를 반환했습니다" },
  "文件内容不完整": { tw: "檔案內容不完整", en: "File content is incomplete", ja: "ファイルの内容が不完全です", ko: "파일 내용이 불완전합니다" },
  "压缩包里的文件过多，无法阅读": { tw: "壓縮檔裡的檔案過多，無法閱讀", en: "Too many files inside this archive to open", ja: "アーカイブ内のファイル数が多すぎて開けません", ko: "압축 파일 안의 항목이 너무 많아 열 수 없습니다" },
  "压缩包里包含异常路径": { tw: "壓縮檔裡包含異常路徑", en: "The archive contains an unsafe path", ja: "アーカイブに不正なパスが含まれています", ko: "압축 파일에 안전하지 않은 경로가 있습니다" },
  "压缩包解压后体积太大，超出了预览限制": { tw: "壓縮檔解壓後體積太大，超出了預覽限制", en: "Expanded archive size exceeds preview limits", ja: "展開後のサイズがプレビュー上限を超えています", ko: "압축 해제 크기가 미리보기 한도를 초과했습니다" },
  "暂不支持带密码的压缩包": { tw: "暫不支援帶密碼的壓縮檔", en: "Password-protected archives aren't supported", ja: "暗号化されたアーカイブには対応していません", ko: "암호화된 압축 파일은 지원하지 않습니다" },
  "文档资源不存在：{path}": { tw: "文件資源不存在：{path}", en: "Document resource not found: {path}", ja: "文書のリソースがありません：{path}", ko: "문서 리소스를 찾을 수 없습니다: {path}" },
  "这种图片格式暂时无法预览": { tw: "這種圖片格式暫時無法預覽", en: "This image format can't be previewed", ja: "この画像形式はプレビューできません", ko: "이 이미지 형식은 미리 볼 수 없습니다" },
  "作为漫画阅读": { tw: "以漫畫方式閱讀", en: "Read as comic", ja: "コミックとして読む", ko: "만화로 읽기" },
  "漫画": { tw: "漫畫", en: "Comic", ja: "コミック", ko: "만화" },
  "表格": { tw: "表格", en: "Table", ja: "表", ko: "표" },
  "字幕与歌词": { tw: "字幕與歌詞", en: "Subtitles & lyrics", ja: "字幕と歌詞", ko: "자막 및 가사" },
  "文档结构太复杂，暂时没法直接预览": { tw: "文件結構太複雜，暫時無法直接預覽", en: "Document structure is too complex to preview", ja: "文書構造が複雑すぎて直接プレビューできません", ko: "문서 구조가 너무 복잡해 바로 미리 볼 수 없습니다" },
} satisfies Messages;
