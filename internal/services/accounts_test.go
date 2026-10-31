package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tonyflux/pkp/internal/drive"
)

// accountsAPI stands in for PikPak with several accounts. Each login has a
// password and a server user id (two logins may share one, as an email and
// a phone number of one account do); tokens name their login, so every API
// call is answered as the account whose token it carries. An account set
// dead has its tokens and its password refused, the way a session the
// server ended and a password changed elsewhere are.
type accountsAPI struct {
	*httptest.Server

	mu        sync.Mutex
	passwords map[string]string // login → password
	subs      map[string]string // login → user id
	dead      map[string]bool   // login → its session and password are refused
	// hold, when set, keeps sign-ins waiting until it is closed.
	hold   chan struct{}
	signIn chan string // logins as their sign-in requests arrive, when set
}

func newAccountsAPI(t *testing.T) *accountsAPI {
	t.Helper()
	a := &accountsAPI{passwords: map[string]string{}, subs: map[string]string{}, dead: map[string]bool{}}
	a.Server = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.Close)
	return a
}

func (a *accountsAPI) add(login, password, sub string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.passwords[login], a.subs[login] = password, sub
}

func (a *accountsAPI) kill(login string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dead[login] = true
}

func (a *accountsAPI) serve(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, body string) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
	switch r.URL.Path {
	case "/v1/shield/captcha/init":
		io.WriteString(w, `{"captcha_token":"c","expires_in":300}`)
		return
	case "/v1/auth/signin":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		login := body["username"]
		a.mu.Lock()
		hold, seen := a.hold, a.signIn
		a.mu.Unlock()
		if seen != nil {
			seen <- login
		}
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		a.mu.Lock()
		password, known := a.passwords[login]
		sub, dead := a.subs[login], a.dead[login]
		a.mu.Unlock()
		if !known || dead || password != body["password"] {
			refuse(http.StatusBadRequest, `{"error":"password_not_match","error_code":4002,"error_description":"wrong password for `+login+`"}`)
			return
		}
		fmt.Fprintf(w, `{"token_type":"Bearer","access_token":"a:%s","refresh_token":"r:%s","expires_in":7200,"sub":%q}`, login, login, sub)
		return
	case "/v1/auth/token":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		login := strings.TrimPrefix(body["refresh_token"], "r:")
		a.mu.Lock()
		dead := a.dead[login]
		a.mu.Unlock()
		if dead {
			refuse(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"refresh token expired"}`)
			return
		}
		fmt.Fprintf(w, `{"token_type":"Bearer","access_token":"a:%s","refresh_token":"r:%s","expires_in":7200}`, login, login)
		return
	}
	login := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer a:")
	a.mu.Lock()
	_, known := a.passwords[login]
	dead := a.dead[login]
	a.mu.Unlock()
	if !known || dead {
		refuse(http.StatusUnauthorized, `{"error":"unauthenticated","error_code":16}`)
		return
	}
	switch r.URL.Path {
	case "/drive/v1/tasks":
		fmt.Fprintf(w, `{"tasks":[{"id":"task-of-%s","name":"clip","phase":"PHASE_TYPE_RUNNING"}],"next_page_token":""}`, login)
	case "/drive/v1/about":
		io.WriteString(w, `{"quota":{"limit":"100","usage":"10"}}`)
	case "/v1/user/me":
		fmt.Fprintf(w, `{"name":"nick of %s","picture":""}`, login)
	default:
		io.WriteString(w, `{"files":[]}`)
	}
}

func startAccounts(t *testing.T, dir string, api *accountsAPI, vault PasswordVault) *DriveService {
	t.Helper()
	return startSignInService(t, dir, &signInServer{Server: api.Server}, vault)
}

func accountIDs(svc *DriveService) []string {
	var ids []string
	for _, a := range svc.Accounts() {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestCreateFolderUsesSpecifiedAccount(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("a@example.com", "pw-a", "user-a")
	api.add("b@example.com", "pw-b", "user-b")
	var authorization string
	api.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/drive/v1/files" {
			authorization = r.Header.Get("Authorization")
			if authorization != "Bearer a:b@example.com" {
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":"permission_denied"}`)
				return
			}
			io.WriteString(w, `{"file":{"kind":"drive#folder","id":"b-folder","name":"Projects","parent_id":"b-parent"}}`)
			return
		}
		api.serve(w, r)
	})
	svc := startAccounts(t, t.TempDir(), api, &memoryVault{})
	a := signIn(t, svc, SignInRequest{Login: "a@example.com", Password: "pw-a"}).Account.ID
	b := signIn(t, svc, SignInRequest{Login: "b@example.com", Password: "pw-b"}).Account.ID
	if _, err := svc.SwitchAccount(a); err != nil {
		t.Fatal(err)
	}
	entry, err := svc.CreateFolder(b, "b-parent", "Projects")
	if err != nil {
		t.Fatalf("create folder in B while A is current: %v", err)
	}
	if authorization != "Bearer a:b@example.com" {
		t.Errorf("authorization = %q, want B's token", authorization)
	}
	if entry.ID != "b-folder" || entry.Name != "Projects" || !entry.Folder {
		t.Errorf("entry = %+v, want B's created folder", entry)
	}
	for _, account := range []string{a, b} {
		idx := svc.byKey(account).index()
		if idx == nil {
			t.Fatalf("account %s index did not open", account)
		}
		row, found, err := idx.store.Get(entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if found != (account == b) {
			t.Errorf("account %s has created folder = %v, want %v", account, found, account == b)
		}
		if found && row.ParentID != "b-parent" {
			t.Errorf("created folder parent = %q, want b-parent", row.ParentID)
		}
	}
	if svc.Status().AccountID != a {
		t.Error("creating a folder in B changed the current account")
	}
}

// Several accounts stay signed in, one on screen, and come back that way:
// adding one keeps the others, signing an account in again — under the same
// login or another one of its logins — renews it in place instead of
// listing it twice, and a relaunch restores every account with the last one
// on screen. Passwords and tokens stay out of the account list and the
// settings file.
func TestAccountsAreAddedOnceAndRestored(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("main@example.com", "pw-main", "user-main")
	api.add("+8613800000000", "pw-phone", "user-main") // the same account by phone
	api.add("movie@example.com", "pw-movie", "user-movie")
	vault := &memoryVault{}
	dir := t.TempDir()
	svc := startAccounts(t, dir, api, vault)

	main := signIn(t, svc, SignInRequest{Login: "main@example.com", Password: "pw-main", SignInOptions: SignInOptions{Remember: true}})
	movie := signIn(t, svc, SignInRequest{Login: "movie@example.com", Password: "pw-movie", Label: "影视收藏"})
	if movie.Existed || svc.Status().AccountID != movie.Account.ID {
		t.Fatalf("adding a second account: %+v, status %+v, want it new and on screen", movie, svc.Status())
	}
	if got := accountIDs(svc); len(got) != 2 {
		t.Fatalf("accounts = %v, want both", got)
	}

	again := signIn(t, svc, SignInRequest{Login: "+8613800000000", Password: "pw-phone"})
	if !again.Existed || again.Account.ID != main.Account.ID {
		t.Errorf("the main account by phone came back as %+v, want the main account renewed", again.Account)
	}
	if got := accountIDs(svc); len(got) != 2 {
		t.Errorf("accounts after signing the main account in again = %v, want still two", got)
	}
	if svc.Status().AccountID != main.Account.ID {
		t.Errorf("on screen: %q, want the account just signed in", svc.Status().AccountID)
	}

	if _, err := svc.SwitchAccount(movie.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RenameAccount(main.Account.ID, "  主账号  "); err != nil {
		t.Fatal(err)
	}
	relaunched := startAccounts(t, dir, api, vault)
	got := relaunched.Accounts()
	if len(got) != 2 || !got[1].Current || got[1].Label != "影视收藏" || got[0].Label != "主账号" {
		t.Fatalf("after relaunch: %+v, want both accounts, labels kept, the movie account on screen", got)
	}
	if !relaunched.Status().Configured {
		t.Errorf("relaunch: %+v, want the account on screen signed in", relaunched.Status())
	}

	for _, name := range []string{accountsFileName, "settings.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"pw-main", "pw-phone", "pw-movie", "a:main@", "r:main@", "a:movie@", "r:movie@"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s holds %q", name, secret)
			}
		}
	}
}

// Sign In Again that comes back as another account changes nothing: the account
// waiting for its password keeps waiting, with its stored session and its
// place, and the other account and the screen are as they were.
func TestSigningBackInAsAnotherAccountIsRefused(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("work@example.com", "pw-work", "user-work")
	api.add("main@example.com", "pw-main", "user-main")
	dir := t.TempDir()
	svc := startAccounts(t, dir, api, &memoryVault{})

	work := signIn(t, svc, SignInRequest{Login: "work@example.com", Password: "pw-work"}).Account.ID
	main := signIn(t, svc, SignInRequest{Login: "main@example.com", Password: "pw-main"}).Account.ID
	api.kill("work@example.com")
	if _, err := svc.Tasks(work, ""); !errors.Is(err, drive.ErrPasswordRequired) {
		t.Fatalf("work account's tasks: %v, want its session ended", err)
	}
	credentials := svc.credentialsPath(work)
	before, err := os.ReadFile(credentials)
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.SignIn(SignInRequest{Login: "main@example.com", Password: "pw-main", Account: work})
	if err != nil || res.Failure != "mismatch" {
		t.Fatalf("signing the work account back in as the main one: %+v %v, want a mismatch", res, err)
	}
	after, _ := os.ReadFile(credentials)
	if !bytes.Equal(before, after) {
		t.Error("the refused sign-in replaced the work account's stored session")
	}
	if accounts := svc.Accounts(); len(accounts) != 2 || !accounts[0].NeedsPassword || accounts[1].NeedsPassword {
		t.Errorf("accounts = %+v, want work still waiting and main still signed in", accounts)
	}
	if st := svc.Status(); st.AccountID != main || !st.Configured {
		t.Errorf("on screen: %+v, want the main account untouched", st)
	}
}

// One account's session ending stops only that account: its calls ask for
// its password while the other account's tasks — read through the other
// account's own session, whichever is on screen — keep coming.
func TestAnEndedSessionLeavesOtherAccountsWorking(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("a@example.com", "pw-a", "user-a")
	api.add("b@example.com", "pw-b", "user-b")
	svc := startAccounts(t, t.TempDir(), api, &memoryVault{})
	a := signIn(t, svc, SignInRequest{Login: "a@example.com", Password: "pw-a"}).Account.ID
	b := signIn(t, svc, SignInRequest{Login: "b@example.com", Password: "pw-b"}).Account.ID

	page, err := svc.Tasks(a, "")
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "task-of-a@example.com" || page.Tasks[0].Account != a {
		t.Fatalf("a's tasks with b on screen = %+v %v, want a's own, marked as a's", page, err)
	}

	api.kill("a@example.com")
	if _, err := svc.Tasks(a, ""); !errors.Is(err, drive.ErrPasswordRequired) {
		t.Fatalf("a's tasks after its session died: %v, want ErrPasswordRequired", err)
	}
	if err := svc.RetryUpload(a, "u1"); !errors.Is(err, drive.ErrPasswordRequired) {
		t.Errorf("acting on a's uploads: %v, want ErrPasswordRequired", err)
	}
	page, err = svc.Tasks(b, "")
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Account != b {
		t.Errorf("b's tasks = %+v %v, want them unaffected", page, err)
	}
	if st := svc.Status(); st.AccountID != b || !st.Configured {
		t.Errorf("on screen: %+v, want b still signed in", st)
	}
}

// A sign-in called off while the server is still answering keeps nothing:
// no account is added, the one on screen stays, and nothing is remembered.
func TestCancelledSignInIsNotKept(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("main@example.com", "pw-main", "user-main")
	api.add("late@example.com", "pw-late", "user-late")
	vault := &memoryVault{}
	svc := startAccounts(t, t.TempDir(), api, vault)
	main := signIn(t, svc, SignInRequest{Login: "main@example.com", Password: "pw-main"}).Account.ID

	api.mu.Lock()
	api.hold, api.signIn = make(chan struct{}), make(chan string, 1)
	hold, arrived := api.hold, api.signIn
	api.mu.Unlock()
	done := make(chan SignInResult, 1)
	go func() {
		res, err := svc.SignIn(SignInRequest{Login: "late@example.com", Password: "pw-late", SignInOptions: SignInOptions{Remember: true}})
		if err != nil {
			t.Errorf("sign in: %v", err)
		}
		done <- res
	}()
	<-arrived
	svc.CancelSignIn()
	close(hold)
	if res := <-done; res.Failure != "cancelled" {
		t.Fatalf("result = %+v, want cancelled", res)
	}
	if got := accountIDs(svc); len(got) != 1 || got[0] != main {
		t.Errorf("accounts = %v, want only the main one", got)
	}
	if st := svc.Status(); st.AccountID != main {
		t.Errorf("on screen: %q, want the main account", st.AccountID)
	}
	if _, err := vault.Password("late@example.com"); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("the cancelled sign-in left a password: %v", err)
	}
}

// The form says why a sign-in failed without the server's own words: a
// wrong password and an unreachable server are different failures.
func TestSignInFailuresAreNamed(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("main@example.com", "pw-main", "user-main")
	svc := startAccounts(t, t.TempDir(), api, &memoryVault{})

	res, err := svc.SignIn(SignInRequest{Login: "main@example.com", Password: "wrong"})
	if err != nil || res.Failure != "credentials" || strings.Contains(res.Message, "wrong password") {
		t.Errorf("wrong password: %+v %v, want a credentials failure in the form's own words", res, err)
	}
	if res.Status.AccountID != "" || len(svc.Accounts()) != 0 {
		t.Errorf("a refused sign-in added %+v", svc.Accounts())
	}

	api.Close()
	res, err = svc.SignIn(SignInRequest{Login: "main@example.com", Password: "pw-main"})
	if err != nil || res.Failure != "network" {
		t.Errorf("server unreachable: %+v %v, want a network failure", res, err)
	}
}

// Removing an account forgets it on this computer — stored session, index,
// upload records, play record, remembered password — and puts the most
// recently used signed-in account on screen; removing the last one leaves
// no account and an empty list.
func TestRemovingAccountsLeavesNoTrace(t *testing.T) {
	api := newAccountsAPI(t)
	for _, login := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		api.add(login, "pw", "user-"+login)
	}
	vault := &memoryVault{}
	dir := t.TempDir()
	svc := startAccounts(t, dir, api, vault)
	a := signIn(t, svc, SignInRequest{Login: "a@example.com", Password: "pw", SignInOptions: SignInOptions{Remember: true}}).Account.ID
	b := signIn(t, svc, SignInRequest{Login: "b@example.com", Password: "pw"}).Account.ID
	c := signIn(t, svc, SignInRequest{Login: "c@example.com", Password: "pw"}).Account.ID
	if _, err := svc.SwitchAccount(a); err != nil { // c, b, then a most recently used
		t.Fatal(err)
	}
	if _, err := svc.SwitchAccount(b); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SwitchAccount(a); err != nil {
		t.Fatal(err)
	}
	api.kill("b@example.com")
	_, _ = svc.Tasks(b, "") // b's session ends: it is no longer a fallback

	if err := svc.byKey(a).playLog().opened(playRecord{FileID: "f1", Name: "x.mkv"}); err != nil {
		t.Fatal(err)
	}
	// What earlier uploads leave: the journal and a resumable-upload checkpoint.
	journal := svc.accountFile(a, "uploads-", ".json")
	checkpoint := filepath.Join(svc.accountFile(a, "uploads-", ".resume"), "u1")
	if err := os.WriteFile(journal, []byte(`{"version":1,"tasks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(checkpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{
		svc.credentialsPath(a),
		svc.accountFile(a, "plays-", ".json"),
		journal,
		checkpoint,
		svc.accountFile(a, "index-", ".sqlite"),
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("before removal %s: %v", f, err)
		}
	}

	status, err := svc.RemoveAccount(a)
	if err != nil {
		t.Fatal(err)
	}
	if status.AccountID != c {
		t.Errorf("on screen after removing it: %q, want c, the most recent account still signed in", status.AccountID)
	}
	for _, f := range files {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived the removal: %v", f, err)
		}
	}
	if _, err := vault.Password("a@example.com"); !errors.Is(err, ErrNoSavedPassword) {
		t.Errorf("a's remembered password survived: %v", err)
	}
	if got := accountIDs(startAccounts(t, dir, api, vault)); len(got) != 2 {
		t.Errorf("after relaunch: %v, want b and c", got)
	}

	for _, id := range []string{b, c} {
		if _, err := svc.RemoveAccount(id); err != nil {
			t.Fatal(err)
		}
	}
	if st := svc.Status(); st.AccountID != "" || st.Configured {
		t.Errorf("after removing every account: %+v, want none on screen", st)
	}
	if got := accountIDs(startAccounts(t, dir, api, vault)); len(got) != 0 {
		t.Errorf("after relaunch: %v, want no account", got)
	}
}

// Where a video was left goes into the play record of the account it was
// opened for, even when another account is on screen and has a file with
// the same id in its own record.
func TestPlaybackPositionStaysWithItsAccount(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("a@example.com", "pw", "user-a")
	api.add("b@example.com", "pw", "user-b")
	svc := startAccounts(t, t.TempDir(), api, &memoryVault{})
	a := signIn(t, svc, SignInRequest{Login: "a@example.com", Password: "pw"}).Account.ID
	b := signIn(t, svc, SignInRequest{Login: "b@example.com", Password: "pw"}).Account.ID
	for _, id := range []string{a, b} {
		if err := svc.byKey(id).playLog().opened(playRecord{FileID: "f1", Name: "same.mkv", Position: 5, Duration: 100}); err != nil {
			t.Fatal(err)
		}
	}

	svc.notePlayback(a, "f1", 42, 100) // b is on screen
	if got := svc.byKey(a).playLog().list(); got[0].Position != 42 {
		t.Errorf("a's record = %+v, want position 42", got[0])
	}
	if got := svc.byKey(b).playLog().list(); got[0].Position != 5 {
		t.Errorf("b's record = %+v, want it untouched at 5", got[0])
	}
}

// The single session of earlier builds becomes the first account, with its
// remembered sign-in, and keeps working: its credentials file is renamed to
// the account's own and nothing asks for a password.
func TestEarlierSessionBecomesTheFirstAccount(t *testing.T) {
	api := newAccountsAPI(t)
	api.add("user@example.com", "pw", "user-1")
	dir := t.TempDir()
	device := drive.DeviceIDFor("user@example.com")
	legacy := drive.FileStore{Path: filepath.Join(dir, legacyCredentials)}
	if err := legacy.Save(drive.Token{Username: "user@example.com", DeviceID: device, RefreshToken: "r:user@example.com", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"signInAccount":"user@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	vault := &memoryVault{passwords: map[string]string{"user@example.com": "pw"}}

	svc := startAccounts(t, dir, api, vault)
	accounts := svc.Accounts()
	if len(accounts) != 1 || accounts[0].ID != device || !accounts[0].Current || accounts[0].Login != "user@example.com" {
		t.Fatalf("accounts = %+v, want the earlier session as the account on screen", accounts)
	}
	if _, err := os.Stat(legacy.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("credentials.json is still there: %v", err)
	}
	if _, err := svc.Quota(); err != nil {
		t.Errorf("first call on the adopted session: %v", err)
	}
	if saved := svc.SavedSignIn(device); !saved.Remember || !saved.HasPassword {
		t.Errorf("the account lost its remembered sign-in: %+v", saved)
	}
}
