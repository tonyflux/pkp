package services

import (
	"slices"
	"testing"

	"github.com/tonyflux/pkp/internal/index"
)

func TestSavedSearchKeepsAudioCategoryAndExactFolder(t *testing.T) {
	svc := aiSearchService(t, `{"events":[]}`, nil)
	audio := indexedVideo("audio", "folder", "Ocean.mp3", 10, "2026-10-23T00:00:00Z")
	audio.Category = "audio"
	other := audio
	other.ID, other.ParentID = "outside", ""
	fillIndex(t, svc, []index.Row{indexedFolder("folder", "", "Music"), audio, other, indexedVideo("video", "folder", "Ocean.mkv", 10, "2026-10-23T00:00:00Z")})
	result, err := svc.SearchIndex("", AIQuery{Category: "audio", FolderID: "folder", Keywords: []string{"Ocean"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hitIDs(result.Hits), []string{"audio"}) {
		t.Fatalf("audio search widened: %+v", result)
	}
	otherFiles, err := svc.SearchIndex("", AIQuery{Category: "otherfiles", Keywords: []string{"Ocean"}})
	if err != nil {
		t.Fatal(err)
	}
	if otherFiles.Total != 0 {
		t.Fatalf("other files included audio/video: %+v", otherFiles)
	}
}
