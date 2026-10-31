package services

import (
	"github.com/tonyflux/pkp/internal/drive"
	"testing"
)

func TestReaderFormatsRequireCompleteFiles(t *testing.T) {
	for _, ext := range []string{"epub", "cbz", "docx", "xlsx", "csv", "tsv", "txt", "srt", "lrc", "mdown"} {
		f := drive.File{Kind: drive.KindFile, Name: "read." + ext, Phase: drive.PhaseComplete}
		if previewOf(f) == "" {
			t.Errorf("%s cannot open", ext)
		}
		f.Phase = "PHASE_TYPE_RUNNING"
		if previewOf(f) != "" {
			t.Errorf("%s offered before complete", ext)
		}
	}
	for _, ext := range []string{"doc", "xls", "ppt", "pptx", "cbr", "mobi"} {
		if previewOf(drive.File{Kind: drive.KindFile, Name: "legacy." + ext}) != "" {
			t.Errorf("%s incorrectly advertised as readable", ext)
		}
	}
}
