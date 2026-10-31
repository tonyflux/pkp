import type { Messages } from "./types";

export default {
  "表格阅读工具": { tw: "表格閱讀工具", en: "Table reading tools", ja: "表の閲覧ツール", ko: "표 읽기 도구" },
  "table|只读": { tw: "唯讀", en: "Read only", ja: "読み取り専用", ko: "읽기 전용" },
  "table|工作表": { tw: "工作表", en: "Sheet", ja: "シート", ko: "시트" },
  "table|编码": { tw: "編碼", en: "Encoding", ja: "文字コード", ko: "문자 인코딩" },
  "table|分隔符": { tw: "分隔符號", en: "Delimiter", ja: "区切り文字", ko: "구분 기호" },
  "table|逗号": { tw: "逗號", en: "Comma", ja: "コンマ", ko: "쉼표" },
  "table|制表符": { tw: "定位字元", en: "Tab", ja: "タブ", ko: "탭" },
  "table|分号": { tw: "分號", en: "Semicolon", ja: "セミコロン", ko: "세미콜론" },
  "table|竖线": { tw: "直線", en: "Pipe", ja: "縦棒", ko: "세로줄" },
  "在整个工作表中查找": { tw: "在整個工作表中尋找", en: "Find in the entire sheet", ja: "シート全体から検索", ko: "전체 시트에서 찾기" },
  "table|上一个匹配": { tw: "上一個符合項目", en: "Previous match", ja: "前の一致", ko: "이전 일치 항목" },
  "table|下一个匹配": { tw: "下一個符合項目", en: "Next match", ja: "次の一致", ko: "다음 일치 항목" },
  "table|行号": { tw: "列號", en: "Row", ja: "行番号", ko: "행 번호" },
  "table|前往": { tw: "前往", en: "Go", ja: "移動", ko: "이동" },
  "table|正在读取表格…": { tw: "正在讀取表格…", en: "Reading table…", ja: "表を読み込み中…", ko: "표 읽는 중…" },
  "表格共 {rows} 行、{columns} 列": { tw: "表格共 {rows} 列、{columns} 欄", en: "{rows} rows × {columns} columns", ja: "{rows} 行 × {columns} 列", ko: "{rows}행 × {columns}열" },
  "table|没有匹配项": { tw: "沒有符合項目", en: "No matches", ja: "一致する項目はありません", ko: "일치하는 항목 없음" },
  "匹配位置：{cell}": { tw: "符合位置：{cell}", en: "Match: {cell}", ja: "一致位置：{cell}", ko: "일치 위치: {cell}" },
  "table|空表格": { tw: "空白表格", en: "Empty table", ja: "空の表", ko: "빈 표" },
  "table|所选单元格内容": { tw: "所選儲存格內容", en: "Selected cell contents", ja: "選択したセルの内容", ko: "선택한 셀 내용" },
  "table|真": { tw: "真", en: "TRUE", ja: "真", ko: "참" },
  "table|假": { tw: "假", en: "FALSE", ja: "偽", ko: "거짓" },
  "仅显示已保存的单元格值，不执行公式或宏；日期按工作簿值显示，部分自定义数字格式不受支持。": {
    tw: "僅顯示已儲存的儲存格值，不執行公式或巨集；日期依工作簿值顯示，部分自訂數字格式不受支援。",
    en: "Saved cell values only; formulas and macros are never executed. Dates use workbook values; some custom number formats are not supported.",
    ja: "保存済みのセル値のみを表示し、数式やマクロは実行しません。日付はブック内の値を使用します。一部のユーザー定義数値書式には対応していません。",
    ko: "저장된 셀 값만 표시하며 수식이나 매크로는 실행하지 않습니다. 날짜는 통합 문서 값을 사용하며 일부 사용자 지정 숫자 형식은 지원하지 않습니다.",
  },
  "无法按所选编码解码，请切换字符编码后重试。": {
    tw: "無法依所選編碼解碼，請切換字元編碼後重試。", en: "Cannot decode with this encoding. Select another character encoding and try again.", ja: "選択した文字コードで読み込めません。文字コードを変更して再試行してください。", ko: "선택한 인코딩으로 읽을 수 없습니다. 문자 인코딩을 바꾸고 다시 시도하세요.",
  },
  "分隔文本的引号格式不正确，请检查分隔符及成对引号。": {
    tw: "分隔文字的引號格式不正確，請檢查分隔符號及成對引號。", en: "Invalid quoting in delimited text. Check the delimiter and paired quotes.", ja: "区切りテキストの引用符の形式が正しくありません。区切り文字と引用符の対応を確認してください。", ko: "구분된 텍스트의 따옴표 형식이 잘못되었습니다. 구분 기호와 따옴표 쌍을 확인하세요.",
  },
  "无法读取此工作簿，文件可能损坏、加密或包含不支持的结构。": {
    tw: "無法讀取此工作簿，檔案可能損毀、加密或包含不支援的結構。", en: "Cannot read this workbook. It may be damaged, encrypted, or contain unsupported structures.", ja: "このブックを読み込めません。破損、暗号化、または未対応の構造が含まれている可能性があります。", ko: "이 통합 문서를 읽을 수 없습니다. 손상되었거나 암호화되었거나 지원하지 않는 구조가 포함되었을 수 있습니다.",
  },
  "表格超过安全读取上限：文本 16 MiB、工作簿 20 MiB、展开 32 MiB、10 万行、512 列或 100 万个网格单元；未截断显示。请用电子表格应用打开。": {
    tw: "表格超過安全讀取上限：文字 16 MiB、工作簿 20 MiB、展開 32 MiB、10 萬列、512 欄或 100 萬個網格儲存格；未截斷顯示。請用試算表應用程式開啟。",
    en: "Table exceeds safe limits: 16 MiB text, 20 MiB workbook, 32 MiB expanded, 100,000 rows, 512 columns, or 1,000,000 grid cells. No truncated data is shown. Open in a spreadsheet app.",
    ja: "安全な読み込み上限（テキスト 16 MiB、ブック 20 MiB、展開後 32 MiB、10 万行、512 列、または 100 万セル）を超えています。途中までのデータは表示しません。表計算アプリで開いてください。",
    ko: "안전한 읽기 한도(텍스트 16 MiB, 통합 문서 20 MiB, 압축 해제 32 MiB, 10만 행, 512열 또는 100만 격자 셀)를 초과했습니다. 잘린 데이터는 표시하지 않습니다. 스프레드시트 앱에서 여세요.",
  },
} satisfies Messages;
