package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/tonyflux/pkp/internal/applock"
)

// fakeSystem stands in for the system's authentication: it answers with
// result and counts the dialogs it was asked for.
type fakeSystem struct {
	available atomic.Bool
	result    atomic.Int32
	asked     atomic.Int32
}

func lockSystem(t *testing.T) *fakeSystem {
	t.Helper()
	sys := &fakeSystem{}
	sys.available.Store(true)
	sys.result.Store(int32(applock.Verified))
	oldAvailable, oldAuth := lockAvailable, lockAuthenticate
	lockAvailable = func() (bool, string) {
		if sys.available.Load() {
			return true, ""
		}
		return false, "这台 Mac 没有设置登录密码，无法使用系统身份验证"
	}
	lockAuthenticate = func(unsafe.Pointer, string) applock.Outcome {
		sys.asked.Add(1)
		return applock.Outcome{Result: applock.Result(sys.result.Load()), Message: "已取消验证"}
	}
	t.Cleanup(func() { lockAvailable, lockAuthenticate = oldAvailable, oldAuth })
	return sys
}

func lockDrive(t *testing.T, config string) *DriveService {
	t.Helper()
	return NewDriveService(DriveOptions{ConfigDir: config, NoIndexSync: true, Logger: quiet()}, nil)
}

// memLockStore keeps the Separate password verifier in memory: tests never reach the
// login keychain.
type memLockStore struct{ verifier string }

func (m *memLockStore) Load() (string, error) {
	if m.verifier == "" {
		return "", errNoLockSecret
	}
	return m.verifier, nil
}
func (m *memLockStore) Save(v string) error { m.verifier = v; return nil }
func (m *memLockStore) Delete() error       { m.verifier = ""; return nil }

// lockService is an AppLockService over drive with its verifier in memory.
func lockService(drive *DriveService, store *memLockStore) *AppLockService {
	l := NewAppLockService(drive, nil, nil)
	l.secrets = store
	return l
}

// fakeClock moves only when told, for the cooldowns.
func fakeClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old := lockClock
	lockClock = func() time.Time { return now }
	t.Cleanup(func() { lockClock = old })
	return &now
}

func TestAppLockTurnsOnOnlyAfterTheOwnerIsConfirmed(t *testing.T) {
	sys := lockSystem(t)
	config := t.TempDir()
	drive := lockDrive(t, config)
	l := NewAppLockService(drive, nil, nil)
	if l.Locked() || l.State().Enabled {
		t.Fatal("a computer without the setting starts locked or enabled")
	}
	if err := l.Lock(); err == nil {
		t.Fatal("Lock now locked with App lock off")
	}

	sys.result.Store(int32(applock.Cancelled))
	got, err := l.SetEnabled(context.Background(), true, "")
	if err != nil || got.Result != "cancelled" || got.State.Enabled || drive.LocalSettings().AppLock {
		t.Fatalf("cancelled dialog: %+v, %v, saved %v", got, err, drive.LocalSettings().AppLock)
	}

	sys.result.Store(int32(applock.Verified))
	got, err = l.SetEnabled(context.Background(), true, "")
	if err != nil || got.Result != "ok" || !got.State.Enabled || !drive.LocalSettings().AppLock {
		t.Fatalf("verified: %+v, %v", got, err)
	}
	if sys.asked.Load() != 2 {
		t.Fatalf("dialogs asked = %d, want 2", sys.asked.Load())
	}
	if l.Locked() {
		t.Fatal("switching on locked at once")
	}

	// The next start of PKP opens locked; with the system unable to
	// authenticate it does not, or nothing could ever open it.
	if !NewAppLockService(lockDrive(t, config), nil, nil).Locked() {
		t.Fatal("PKP with App lock on started unlocked")
	}
	sys.available.Store(false)
	if NewAppLockService(lockDrive(t, config), nil, nil).Locked() {
		t.Fatal("PKP started locked though the system cannot authenticate")
	}
}

func TestAppLockStaysLockedUntilTheSystemSaysYes(t *testing.T) {
	sys := lockSystem(t)
	drive := lockDrive(t, t.TempDir())
	if err := drive.updateSettings(func(s *LocalSettings) { s.AppLock = true }); err != nil {
		t.Fatal(err)
	}
	l := NewAppLockService(drive, nil, nil)
	if !l.Locked() {
		t.Fatal("not locked at start")
	}
	if _, err := l.SetEnabled(context.Background(), false, ""); err == nil {
		t.Fatal("switched off while locked")
	}
	if err := l.SetDelay(15); err == nil {
		t.Fatal("delay changed while locked")
	}

	for _, r := range []applock.Result{applock.Cancelled, applock.Failed, applock.Unavailable} {
		sys.result.Store(int32(r))
		if got := l.Unlock(context.Background()); got.Result == "ok" || !got.State.Locked || !l.Locked() {
			t.Fatalf("result %d unlocked: %+v", r, got)
		}
	}
	sys.result.Store(int32(applock.Verified))
	if got := l.Unlock(context.Background()); got.Result != "ok" || got.State.Locked || l.Locked() {
		t.Fatalf("verified stayed locked: %+v", got)
	}
	if err := l.Lock(); err != nil || !l.Locked() {
		t.Fatalf("Lock now: %v, locked %v", err, l.Locked())
	}
}

func TestAppLockDelayStaysWithinADay(t *testing.T) {
	lockSystem(t)
	config := t.TempDir()
	drive := lockDrive(t, config)
	l := NewAppLockService(drive, nil, nil)
	if got := l.State().Delay; got != 5 {
		t.Fatalf("default delay = %d, want 5", got)
	}
	for _, bad := range []int{-1, 24*60 + 1} {
		if err := l.SetDelay(bad); err == nil {
			t.Fatalf("accepted %d minutes", bad)
		}
	}
	// Immediately, a custom 45 minutes and the whole day are kept as typed.
	for _, good := range []int{0, 45, 24 * 60} {
		if err := l.SetDelay(good); err != nil || drive.LocalSettings().AppLockDelay != good {
			t.Fatalf("%d minutes not saved: %v (saved %d)", good, err, drive.LocalSettings().AppLockDelay)
		}
	}
	// A hand-edited file out of range falls back to the default.
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"appLock":true,"appLockDelay":5000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := drive.LocalSettings().AppLockDelay; got != 5 {
		t.Fatalf("hand-edited delay = %d, want 5", got)
	}
}

func TestAppLockLocksAfterLeavingUnlessBackInTime(t *testing.T) {
	lockSystem(t)
	old := appLockInstant
	appLockInstant = 30 * time.Millisecond
	t.Cleanup(func() { appLockInstant = old })
	drive := lockDrive(t, t.TempDir())
	if err := drive.updateSettings(func(s *LocalSettings) { s.AppLock, s.AppLockDelay = true, 0 }); err != nil {
		t.Fatal(err)
	}
	l := NewAppLockService(drive, nil, nil)
	l.unlock()

	l.left()
	l.returned()
	time.Sleep(4 * appLockInstant)
	if l.Locked() {
		t.Fatal("locked although the user came back in time")
	}

	l.left()
	deadline := time.Now().Add(time.Second)
	for !l.Locked() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !l.Locked() {
		t.Fatal("Immediately did not lock after leaving")
	}

	// With the setting off, leaving does nothing.
	l.unlock()
	if err := drive.updateSettings(func(s *LocalSettings) { s.AppLock = false }); err != nil {
		t.Fatal(err)
	}
	l.left()
	time.Sleep(4 * appLockInstant)
	if l.Locked() {
		t.Fatal("locked with App lock off")
	}
}

// gateCall is a Wails runtime request for a binding call by method id.
func gateCall(ctx context.Context, object, id int) *http.Request {
	body := `{"object":` + strconv.Itoa(object) + `,"method":0,"args":{"call-id":"c1","methodID":` + strconv.Itoa(id) + `,"args":[]}}`
	return httptest.NewRequest(http.MethodPost, "/wails/runtime", strings.NewReader(body)).WithContext(ctx)
}

// bindingIDs reads the method ids the generated bindings call a service's
// methods by.
func bindingIDs(t *testing.T, file string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "bindings", "github.com", "tonyflux", "pkp", "internal", "services", file))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{}
	for _, m := range regexp.MustCompile(`export function (\w+)\([^)]*\)[^{]*\{\s*return \$Call\.ByID\((\d+)`).FindAllStringSubmatch(string(data), -1) {
		id, _ := strconv.Atoi(m[2])
		ids[m[1]] = id
	}
	return ids
}

func TestLockGateHoldsThePagesCallsUntilUnlocked(t *testing.T) {
	sys := lockSystem(t)
	drive := lockDrive(t, t.TempDir())
	if err := drive.updateSettings(func(s *LocalSettings) { s.AppLock = true }); err != nil {
		t.Fatal(err)
	}
	l := NewAppLockService(drive, nil, nil)

	lock := bindingIDs(t, "applockservice.ts")
	panel := bindingIDs(t, "menubarservice.ts")
	files := bindingIDs(t, "driveservice.ts")
	if lock["Unlock"] == 0 || panel["State"] == 0 || files["List"] == 0 {
		t.Fatalf("bindings not read: %v %v", lock, files["List"])
	}

	var served atomic.Int32
	gate := LockGate(l)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, r)
		return rec
	}

	// The lock screen's own calls, the panel's, and the runtime's events
	// pass at once: a lock screen that could not reach Unlock would never
	// open again. A held one would be refused once its page gives up.
	passes := func(object, id int) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		return serve(gateCall(ctx, object, id)).Code == http.StatusOK
	}
	for name, id := range lock {
		if !passes(0, id) {
			t.Fatalf("AppLockService.%s held", name)
		}
	}
	if !passes(0, panel["State"]) {
		t.Fatal("MenuBarService.State held")
	}
	if !passes(3, 0) {
		t.Fatal("an event emit held")
	}

	// A file listing waits for the owner, then runs.
	done := make(chan int, 1)
	go func() { done <- serve(gateCall(context.Background(), 0, files["List"])).Code }()
	select {
	case code := <-done:
		t.Fatalf("DriveService.List ran while locked: %d", code)
	case <-time.After(50 * time.Millisecond):
	}
	sys.result.Store(int32(applock.Verified))
	if got := l.Unlock(context.Background()); got.Result != "ok" {
		t.Fatalf("unlock: %+v", got)
	}
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("held call answered %d after unlocking", code)
		}
	case <-time.After(time.Second):
		t.Fatal("held call still waiting after unlocking")
	}

	// A call whose page went away while held is refused, not run.
	l.lock()
	before := served.Load()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	rec := serve(gateCall(ctx, 0, files["List"]))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), errAppLocked.Error()) || served.Load() != before {
		t.Fatalf("abandoned call: %d %q, served %d→%d", rec.Code, rec.Body.String(), before, served.Load())
	}
}

func TestMenuBarShowsOnlyCountsWhileLocked(t *testing.T) {
	up := &UploadTask{}
	up.Name = "家庭录像.mov"
	full := MenuBarState{
		WindowOpen: true,
		Account:    "acc-1",
		Running:    2,
		Attention:  1,
		Inbox:      1,
		InboxItem:  &Arrival{Name: "山海纪行"},
		Cloud:      "ok",
		Item:       MenuBarItem{Kind: "upload", Upload: up},
	}
	got := lockedMenuBarState(full)
	want := MenuBarState{WindowOpen: true, Running: 2, Attention: 1, Inbox: 1, Cloud: "ok", Locked: true}
	if got.Account != "" || got.InboxItem != nil || got.Item.Kind != "" || got.Item.Upload != nil {
		t.Fatalf("locked panel state names something: %+v", got)
	}
	if got.WindowOpen != want.WindowOpen || got.Running != want.Running || got.Attention != want.Attention ||
		got.Inbox != want.Inbox || got.Cloud != want.Cloud || !got.Locked {
		t.Fatalf("locked panel state = %+v, want %+v", got, want)
	}
}

func TestLockVerifierNeverKeepsThePassword(t *testing.T) {
	v, err := newLockVerifier("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v, "correct horse") || !strings.HasPrefix(v, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("verifier = %q", v)
	}
	other, _ := newLockVerifier("correct horse")
	if other == v {
		t.Fatal("two verifiers of one password share a salt")
	}
	for pw, want := range map[string]bool{"correct horse": true, "correct horsE": false, "": false} {
		if got, err := lockVerifierMatches(v, pw); err != nil || got != want {
			t.Fatalf("match %q = %v, %v; want %v", pw, got, err, want)
		}
	}
	for _, bad := range []string{"plain", "$argon2id$v=19$m=4194304,t=3,p=4$AAAA$AAAA", "$argon2i$v=19$m=65536,t=3,p=4$AAAA$AAAA"} {
		if _, err := lockVerifierMatches(bad, "x"); err == nil {
			t.Fatalf("accepted record %q", bad)
		}
	}
}

func TestAppLockPasswordCooldownGrowsAndSurvivesARestart(t *testing.T) {
	lockSystem(t)
	now := fakeClock(t)
	config := t.TempDir()
	drive := lockDrive(t, config)
	store := &memLockStore{}
	l := lockService(drive, store)
	const password = "1"

	if r, _ := l.SetPassword(context.Background(), "", ""); r.Result != "invalid" || drive.LocalSettings().AppLockPassword {
		t.Fatalf("empty password: %+v", r)
	}
	if r, err := l.SetPassword(context.Background(), "", password); err != nil || !r.ok() || !r.State.HasPassword {
		t.Fatalf("set: %+v, %v", r, err)
	}
	if r, err := l.SetMethod(context.Background(), lockMethodPassword, ""); err != nil || !r.ok() {
		t.Fatalf("method while off: %+v, %v", r, err)
	}
	if r, err := l.SetEnabled(context.Background(), true, password); err != nil || !r.ok() {
		t.Fatalf("enable: %+v, %v", r, err)
	}
	if err := l.Lock(); err != nil {
		t.Fatal(err)
	}
	if r := l.Unlock(context.Background()); r.ok() {
		t.Fatal("the system unlocked a separate-password lock")
	}

	for i := 1; i < lockMaxFails; i++ {
		r := l.UnlockWithPassword("guess")
		if r.Result != "wrong" || r.State.AttemptsLeft != lockMaxFails-i || !l.Locked() {
			t.Fatalf("guess %d: %+v", i, r)
		}
	}
	r := l.UnlockWithPassword("guess")
	if r.Result != "cooldown" || r.State.CooldownUntil != now.Add(lockCooldown).UnixMilli() {
		t.Fatalf("5th guess: %+v", r)
	}
	// Quitting and starting again keeps the count, and during the cooldown
	// even the right password is not looked at.
	l = lockService(lockDrive(t, config), store)
	if r := l.UnlockWithPassword(password); r.Result != "cooldown" || !l.Locked() {
		t.Fatalf("right password in a cooldown after a restart: %+v", r)
	}

	// The next round costs twice as long.
	*now = now.Add(lockCooldown)
	for range lockMaxFails {
		r = l.UnlockWithPassword("guess")
	}
	if r.Result != "cooldown" || r.State.CooldownUntil != now.Add(2*lockCooldown).UnixMilli() {
		t.Fatalf("second round: %+v", r)
	}
	*now = now.Add(2 * lockCooldown)
	if r := l.UnlockWithPassword(password); !r.ok() || l.Locked() || r.State.AttemptsLeft != lockMaxFails {
		t.Fatalf("right password after the cooldown: %+v", r)
	}
	if got := drive.LocalSettings().AppLockThrottle; got != (LockThrottle{}) {
		t.Fatalf("throttle after success = %+v", got)
	}
}

func TestAppLockChangesAreProvenWithTheWayInNow(t *testing.T) {
	sys := lockSystem(t)
	fakeClock(t)
	drive := lockDrive(t, t.TempDir())
	l := lockService(drive, &memLockStore{})
	ctx := context.Background()

	if r, _ := l.SetEnabled(ctx, true, ""); !r.ok() {
		t.Fatalf("enable under System authentication: %+v", r)
	}
	// A backup password added to a lock that is on asks the system first.
	sys.result.Store(int32(applock.Cancelled))
	if r, _ := l.SetPassword(ctx, "", "open sesame"); r.ok() || r.State.HasPassword {
		t.Fatalf("backup added without the system: %+v", r)
	}
	sys.result.Store(int32(applock.Verified))
	if r, _ := l.SetPassword(ctx, "", "open sesame"); !r.ok() {
		t.Fatalf("backup: %+v", r)
	}
	// Changing it needs the current one.
	if r, _ := l.SetPassword(ctx, "nope nope", "new secret"); r.Result != "wrong" {
		t.Fatalf("change with a wrong current password: %+v", r)
	}
	// Switching to the Separate password is proven with it.
	if r, _ := l.SetMethod(ctx, lockMethodPassword, "nope nope"); r.ok() || drive.LocalSettings().AppLockMethod != lockMethodSystem {
		t.Fatalf("switched with a wrong password: %+v", r)
	}
	if r, _ := l.SetMethod(ctx, lockMethodPassword, "open sesame"); !r.ok() || r.State.ProveWith != lockMethodPassword {
		t.Fatalf("switch to Separate password: %+v", r)
	}
	// Now it is the way in: it cannot be removed, and switching off needs it.
	if _, err := l.RemovePassword("open sesame"); err == nil {
		t.Fatal("removed the only way in")
	}
	asked := sys.asked.Load()
	if r, _ := l.SetEnabled(ctx, false, "nope nope"); r.ok() || !drive.LocalSettings().AppLock || sys.asked.Load() != asked {
		t.Fatalf("switched off with a wrong password, or asked the system: %+v", r)
	}
	// Back to System authentication only once the system answers.
	sys.result.Store(int32(applock.Cancelled))
	if r, _ := l.SetMethod(ctx, lockMethodSystem, ""); r.ok() || drive.LocalSettings().AppLockMethod != lockMethodPassword {
		t.Fatalf("switched to a system that did not answer: %+v", r)
	}
	sys.result.Store(int32(applock.Verified))
	if r, _ := l.SetMethod(ctx, lockMethodSystem, ""); !r.ok() {
		t.Fatalf("switch to System authentication: %+v", r)
	}
	// The backup goes with its password; then the lock is the system's alone.
	if r, err := l.RemovePassword("open sesame"); err != nil || !r.ok() || r.State.HasPassword {
		t.Fatalf("remove backup: %+v, %v", r, err)
	}
}

func TestAppLockBackupPasswordStandsInForAMissingSystem(t *testing.T) {
	sys := lockSystem(t)
	fakeClock(t)
	config := t.TempDir()
	drive := lockDrive(t, config)
	store := &memLockStore{}
	l := lockService(drive, store)
	ctx := context.Background()
	if r, _ := l.SetEnabled(ctx, true, ""); !r.ok() {
		t.Fatalf("enable: %+v", r)
	}
	if r, _ := l.SetPassword(ctx, "", "open sesame"); !r.ok() {
		t.Fatalf("backup: %+v", r)
	}

	// The system's way in is gone: PKP still starts locked, opens with the
	// backup, and keeps it — it is the only way in now.
	sys.available.Store(false)
	l = lockService(lockDrive(t, config), store)
	if !l.Locked() || l.State().ProveWith != lockMethodPassword {
		t.Fatalf("no system but a backup: locked %v, state %+v", l.Locked(), l.State())
	}
	if r := l.UnlockWithPassword("open sesame"); !r.ok() || l.Locked() {
		t.Fatalf("backup unlock: %+v", r)
	}
	if _, err := l.RemovePassword("open sesame"); err == nil {
		t.Fatal("removed the backup while it is the only way in")
	}
}

func TestForgetAndSignOutClearsAccountsAndTheLock(t *testing.T) {
	sys := lockSystem(t)
	fakeClock(t)
	drive := newDriveService(t, func(w http.ResponseWriter, r *http.Request) {
		if !authAndTokenRoutes(w, r) {
			http.NotFound(w, r)
		}
	})
	store := &memLockStore{}
	l := lockService(drive, store)
	ctx := context.Background()
	l.SetPassword(ctx, "", "open sesame")
	l.SetMethod(ctx, lockMethodPassword, "")
	l.SetEnabled(ctx, true, "open sesame")

	if err := l.ForgetAndSignOut(); err == nil {
		t.Fatal("forgot while unlocked")
	}
	if err := l.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := l.ForgetAndSignOut(); err != nil {
		t.Fatal(err)
	}
	s := drive.LocalSettings()
	if l.Locked() || len(drive.Accounts()) != 0 || s.AppLock || s.AppLockPassword || s.AppLockMethod != lockMethodSystem || store.verifier != "" {
		t.Fatalf("after forgetting: locked %v, accounts %d, settings %+v, verifier %q", l.Locked(), len(drive.Accounts()), s, store.verifier)
	}
	if sys.asked.Load() != 0 {
		t.Fatalf("the system was asked %d times under Separate password", sys.asked.Load())
	}
}
