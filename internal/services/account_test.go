package services

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// The subfile filter is a mode on the server ("smart"/"close"), not a switch,
// and an item the server leaves out is at its default — which for the filter
// is on. Writing "true" for it would silently turn filtering off.
func TestTransferPreferencesTranslateServerModes(t *testing.T) {
	stored := map[string]string{"defaultFolderBasedOnPlatform": "true", "filterFilesWhenCreate": "close"}
	var written map[string]string
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/user/v1/settings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&written)
			io.WriteString(w, `{}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"settings": stored})
	})

	prefs, err := svc.TransferPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.SeparateAppFolders || prefs.SmartFilter || prefs.AutoParseTorrent {
		t.Errorf("prefs = %+v", prefs)
	}

	stored = map[string]string{}
	if prefs, _ := svc.TransferPreferences(); !prefs.SmartFilter {
		t.Error("an unset filter must read as the server's default, on")
	}

	if err := svc.SetTransferPreference("smartFilter", true); err != nil {
		t.Fatal(err)
	}
	if written["item"] != "filterFilesWhenCreate" || written["value"] != "smart" {
		t.Errorf("written = %v", written)
	}
	if err := svc.SetTransferPreference("autoParseTorrent", false); err != nil {
		t.Fatal(err)
	}
	if written["item"] != "autoParseTorrent" || written["value"] != "false" {
		t.Errorf("written = %v", written)
	}
	if err := svc.SetTransferPreference("parallelUploads", true); err == nil {
		t.Error("an unknown preference must be rejected, not written")
	}
}

// Usage is listed per client with its sessions nested; the UI looks a
// session up directly, including a WebDAV credential by its username.
func TestTransferQuotaIndexesUsageBySession(t *testing.T) {
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/vip/v1/quantity/list" || r.URL.Query().Get("type") != "transfer" {
			t.Errorf("request = %s", r.URL)
		}
		io.WriteString(w, `{
		  "base": {"vip_status":"ok",
		    "offline":{"total_assets":43980465111040,"assets":5007361773},
		    "download":{"total_assets":"4398046511104","assets":"19089812407"},
		    "upload":{"total_assets":1649267441664,"assets":93},
		    "download_daily":{"total_assets":0,"assets":0}},
		  "apps_summary": {"download":{"total_assets":1099511627776,"assets":2048}},
		  "clients": [
		    {"client_id":"ZWWOmO5xPR83hucV","download":{"assets":2048},
		     "sessions":[{"session_id":"dav-user","download":{"total_assets":0,"assets":2048}}]}
		  ]
		}`)
	})

	q, err := svc.TransferQuota()
	if err != nil {
		t.Fatal(err)
	}
	if q.Base.Downstream.Limit != 4398046511104 || q.Base.Downstream.Used != 19089812407 || q.Base.DailyDownstream.Limit != 0 {
		t.Errorf("base = %+v", q.Base)
	}
	if q.Apps.Downstream.Used != 2048 {
		t.Errorf("apps = %+v", q.Apps)
	}
	if q.Sessions["dav-user"].Downstream.Used != 2048 || q.Clients["ZWWOmO5xPR83hucV"].Downstream.Used != 2048 {
		t.Errorf("sessions = %+v clients = %+v", q.Sessions, q.Clients)
	}
}

// The membership as a real account answers it (2026-10-15): one running
// item and one that ended. Only a running membership carries an end date;
// a limit the server puts on it is passed on in its own words.
func TestMembershipReadsRunningAndEndedItems(t *testing.T) {
	body := `{"data":{"expire":"2029-02-14T01:47:24+08:00","status":"ok","type":"platinum",
	  "restricted":{"result":false,"content":{"text":""}},
	  "vipItem":[
	    {"type":"global","description":"Global Premium","status":"invalid","expire":"2026-01-07T19:08:56+08:00","surplus_day":-281},
	    {"type":"regional","description":"Regional Premium","status":"ok","expire":"2029-02-14T01:47:24+08:00","surplus_day":853}]}}`
	svc := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if authAndTokenRoutes(w, r) {
			return
		}
		if r.URL.Path != "/vip/v1/vip/info" {
			t.Errorf("request = %s", r.URL)
		}
		io.WriteString(w, body)
	})

	m, err := svc.Membership("")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Active || m.Expire != "2029-02-14T01:47:24+08:00" || m.Notice != "" || len(m.Items) != 2 {
		t.Fatalf("membership = %+v", m)
	}
	if g, r := m.Items[0], m.Items[1]; g.Active || g.Kind != "global" || g.DaysLeft != -281 || !r.Active || r.Kind != "regional" || r.DaysLeft != 853 {
		t.Errorf("items = %+v", m.Items)
	}

	body = `{"data":{"expire":"2021-11-27T15:23:19+08:00","status":"invalid","type":"novip",
	  "restricted":{"result":true,"content":{"text":"当前地区无法使用会员"}},
	  "vipItem":[{"type":"regional","description":"Regional Premium","status":"invalid","expire":"2021-11-27T15:23:19+08:00","surplus_day":-1783}]}}`
	m, err = svc.Membership("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Active || m.Expire != "" || m.Notice != "当前地区无法使用会员" || len(m.Items) != 1 || m.Items[0].Active {
		t.Errorf("ended membership = %+v", m)
	}
}
