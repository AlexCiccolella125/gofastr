package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	uiruntime "github.com/DonaldMurillo/gofastr/core-ui/runtime"
	"github.com/DonaldMurillo/gofastr/core-ui/store"
	"github.com/DonaldMurillo/gofastr/internal/chromedptest"
)

// Browser coverage for the local-store module and its three bridges,
// against the real registration and the real runtime: an httptest
// server serves runtime.js, every module by name, the inline
// #gofastr-behaviors block (the shape an export ships), the store's
// manifest script on the extra-script rail, one page, and one RPC
// endpoint wrapped by Upload.Wrap that echoes what it read and writes
// a record back. Mirrors framework/headless/behavior_e2e_test.go.

type e2eDraft struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
	Body  string `json:"body,omitempty"`
}

type e2ePrefs struct {
	Theme string `json:"theme"`
}

// e2eStore declares one store per test process. The app id is fixed
// because the module's storage prefix is what the tests read back;
// New refuses a duplicate, so the declaration is shared through once.
var (
	e2eOnce   sync.Once
	e2eSite   *Store
	e2eDrafts *Collection[e2eDraft]
	e2ePref   *Collection[e2ePrefs]
	e2eSeed   *SeededSignal[e2eDraft]
)

func e2eDeclare(t *testing.T) {
	t.Helper()
	e2eOnce.Do(func() {
		resetForTest()
		e2eSite = New("e2e")
		e2eDrafts = Define[e2eDraft](e2eSite, "drafts", CollectionConfig{
			Version: 2, KeyField: "id", MaxRecordBytes: 512, MaxRecords: 3, MaxBytes: 1024,
			Migrations: []Migration{{Version: 2, Steps: []Step{Rename("body", "text"), Func("drafts-v2")}}},
		})
		e2ePref = Define[e2ePrefs](e2eSite, "prefs", CollectionConfig{Version: 1, Mirror: true})
		e2eSeed = SeedSignal(e2eDrafts, "current", store.JSON[e2eDraft](store.New("e2elocal"), "current", e2eDraft{Title: "server default"}))
	})
}

// e2eServer is the page and the endpoint.
type e2eServer struct {
	srv *httptest.Server
	mu  sync.Mutex
	// what the wrapped handler saw, per request
	seen []e2eSeen
}

type e2eSeen struct {
	Body    string
	Drafts  []Record[e2eDraft]
	Current e2eDraft
	Found   bool
	Theme   string
	Err     error
}

func startE2E(t *testing.T) *e2eServer {
	t.Helper()
	e2eDeclare(t)
	js, err := uiruntime.RuntimeJS()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := uiruntime.Module(BehaviorName); !ok {
		t.Fatalf("%s is not served by runtime.Module", BehaviorName)
	}
	block := uiruntime.BehaviorsJSON()
	e := &e2eServer{}
	up := Send(e2eDrafts.Key("current"), e2ePref)
	mux := http.NewServeMux()
	mux.HandleFunc("/__gofastr/runtime.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(js))
	})
	mux.HandleFunc("/__gofastr/runtime/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/__gofastr/runtime/"), ".js")
		w.Header().Set("Content-Type", "application/javascript")
		if src, ok := uiruntime.Module(name); ok {
			_, _ = w.Write([]byte(src))
			return
		}
		http.NotFound(w, r)
	})
	mux.Handle(e2eSite.ScriptPath(), e2eSite.ScriptHandler())
	// The app's own script on the rail: the func migration, and a
	// counter the tests read.
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(`window.__migrated = 0;
(window.__gofastr._localMigrations = window.__gofastr._localMigrations || {})['drafts-v2'] = (rec) => { window.__migrated++; rec.title = (rec.title || '') + ' (v2)'; return rec; };
window.__errors = []; window.addEventListener('gofastr:local-error', (e) => window.__errors.push(e.detail));
window.__migrations = []; window.addEventListener('gofastr:local-migrated', (e) => window.__migrations.push(e.detail));`))
	})
	mux.Handle("/upload", up.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var s e2eSeen
		s.Body = string(b)
		s.Drafts, s.Err = List(r.Context(), e2eDrafts)
		s.Current, s.Found, _ = Get(r.Context(), e2eDrafts, "current")
		if p, ok, _ := Get(r.Context(), e2ePref, "theme"); ok {
			s.Theme = p.Theme
		}
		e.mu.Lock()
		e.seen = append(e.seen, s)
		e.mu.Unlock()
		if s.Found {
			// Download: the server pushes a record back and a second one.
			_ = Put(w, e2eDrafts, "current", e2eDraft{ID: "current", Title: s.Current.Title + " (server)"})
			_ = Put(w, e2eDrafts, "from-server", e2eDraft{ID: "from-server", Title: "pushed"})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		ClearOnNextLoad(w, r, e2eSite)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><html><head><title>plain</title>`+
			`<script type="application/json" id="gofastr-behaviors">%s</script></head><body>`+
			`<main role="main"><span id="ready">ready</span></main>`+
			`<script src="/__gofastr/runtime.js"></script></body></html>`, block)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		ctx := context.Background()
		seedEl := e2eSeed.Bind(ctx, "p", map[string]string{"id": "seeded"})
		form := `<form id="up" data-fui-rpc="/upload" data-fui-rpc-signal="up-result"` + attrString(up.Attrs()) + `><input name="note" value="hi"><button id="send" type="submit">send</button></form>`
		fmt.Fprintf(w, `<!doctype html><html><head><title>local</title>`+
			`<script type="application/json" id="gofastr-behaviors">%s</script></head><body>`+
			`<main role="main"><span id="ready">ready</span>%s%s<span id="result" data-fui-signal="up-result"></span>`+
			`<a id="away" href="/other">other</a></main>`+
			`<script src="/__gofastr/runtime.js"></script><script src="%s"></script><script src="/app.js"></script></body></html>`,
			block, seedEl, form, e2eSite.ScriptURL())
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func attrString(m map[string]string) string {
	var sb strings.Builder
	for _, k := range sortedKeys(m) {
		sb.WriteString(" " + k + `="` + m[k] + `"`)
	}
	return sb.String()
}

func (e *e2eServer) last(t *testing.T) e2eSeen {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) == 0 {
		t.Fatal("the upload endpoint was never called")
	}
	return e.seen[len(e.seen)-1]
}

func awaitP(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }

// openPage navigates, waits for the page and loads the module the way
// an application script does.
func openPage(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Evaluate(`window.__gofastr.loadModule('local-store')`, nil, awaitP),
	); err != nil {
		t.Fatalf("loading local-store: %v", err)
	}
}

func pollTrue(ctx context.Context, js string) bool {
	for range 60 {
		var v bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &v, awaitP)); err == nil && v {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func evalJSON(t *testing.T, ctx context.Context, js string, into any) {
	t.Helper()
	var raw json.RawMessage
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw, awaitP)); err != nil {
		t.Fatalf("evaluate %s: %v", js, err)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("decode %s = %s: %v", js, raw, err)
		}
	}
}

const draftsJS = `window.__gofastr.localStore('e2e').collection('drafts')`
const prefsJS = `window.__gofastr.localStore('e2e').collection('prefs')`

// A record put on one page load is back on the next, under the
// namespaced primitive key; a second browser never sees it.
func TestE2E_RecordSurvivesReloadAndSessionsAreIsolated(t *testing.T) {
	e := startE2E(t)
	ctx := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, ctx, e.srv.URL+"/")

	var res map[string]any
	evalJSON(t, ctx, draftsJS+`.put({id: 'a', title: 'first'})`, &res)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("put = %v", res)
	}
	var keys []string
	evalJSON(t, ctx, `window.__gofastr.local.keys('local.e2e.drafts:')`, &keys)
	if len(keys) != 1 || keys[0] != "local.e2e.drafts:a" {
		t.Fatalf("primitive keys = %v: the record must live under local.<app>.<collection>:<key>", keys)
	}

	openPage(t, ctx, e.srv.URL+"/")
	var back map[string]any
	evalJSON(t, ctx, draftsJS+`.get('a')`, &back)
	if back["title"] != "first" {
		t.Fatalf("after reload get('a') = %v", back)
	}
	var n int
	evalJSON(t, ctx, draftsJS+`.count()`, &n)
	if n != 1 {
		t.Fatalf("count = %d", n)
	}

	other := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, other, e.srv.URL+"/")
	evalJSON(t, other, draftsJS+`.count()`, &n)
	if n != 0 {
		t.Fatalf("a second browser session sees %d records — storage leaked across sessions", n)
	}
}

// The caps refuse with the documented reason and never throw: a
// record over MaxRecordBytes says 'size', one more than MaxRecords
// says 'full', and each raises gofastr:local-error.
func TestE2E_CapsRefuseWithAReasonNotAnException(t *testing.T) {
	e := startE2E(t)
	ctx := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, ctx, e.srv.URL+"/")

	var res map[string]any
	evalJSON(t, ctx, draftsJS+`.put({id: 'big', title: 'x'.repeat(600)})`, &res)
	if res["ok"] != false || res["reason"] != "size" {
		t.Fatalf("over the record cap: %v, want {ok:false, reason:'size'}", res)
	}
	var allOK bool
	evalJSON(t, ctx, `Promise.all([`+draftsJS+`.put({id:'1',title:'a'}),`+draftsJS+`.put({id:'2',title:'b'}),`+draftsJS+`.put({id:'3',title:'c'})]).then(rs => rs.every(r => r.ok))`, &allOK)
	if !allOK {
		t.Fatal("three records within the caps were refused")
	}
	evalJSON(t, ctx, draftsJS+`.put({id: '4', title: 'd'})`, &res)
	if res["ok"] != false || res["reason"] != "full" {
		t.Fatalf("over MaxRecords: %v, want {ok:false, reason:'full'}", res)
	}
	// Replacing an existing record is not a fourth record.
	evalJSON(t, ctx, draftsJS+`.put({id: '3', title: 'c2'})`, &res)
	if res["ok"] != true {
		t.Fatalf("replacing a record must fit: %v", res)
	}
	evalJSON(t, ctx, draftsJS+`.put({title: 'no key'})`, &res)
	if res["ok"] != false || res["reason"] != "key" {
		t.Fatalf("a record without its key field: %v", res)
	}
	evalJSON(t, ctx, `window.__gofastr.localStore('e2e').collection('nope')`, &res)
	if res != nil {
		t.Fatalf("an undeclared collection must be null, got %v", res)
	}
	var errs []map[string]any
	evalJSON(t, ctx, `window.__errors`, &errs)
	if len(errs) != 3 || errs[0]["reason"] != "size" || errs[1]["reason"] != "full" || errs[2]["reason"] != "key" {
		t.Fatalf("gofastr:local-error events = %v", errs)
	}
	if max, _ := errs[0]["max"].(float64); int(max) != 512 {
		t.Fatalf("the size event must carry the cap: %v", errs[0])
	}
	// list with a filter and an order.
	var rows []map[string]any
	evalJSON(t, ctx, draftsJS+`.list({orderBy: 'title', desc: true, limit: 2})`, &rows)
	if len(rows) != 2 || rows[0]["key"] != "3" || rows[1]["key"] != "2" {
		t.Fatalf("list(orderBy title desc, limit 2) = %v", rows)
	}
	evalJSON(t, ctx, draftsJS+`.list({where: {title: 'a'}})`, &rows)
	if len(rows) != 1 || rows[0]["key"] != "1" {
		t.Fatalf("list(where title=a) = %v", rows)
	}
}

// A collection written at schema v1 is migrated to v2 exactly once:
// the declared rename runs, the host's func runs per record, the
// version is stamped, and a reload runs nothing again.
func TestE2E_MigrationRunsOnce(t *testing.T) {
	e := startE2E(t)
	ctx := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	// Plant v1 data through the primitive, the way an older build of
	// the app would have left it, from a page with NO marker: on the
	// real page the kernel loads the module at boot and the store's
	// migration would race the plant.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(e.srv.URL+"/plain"),
		chromedp.WaitVisible(`#ready`, chromedp.ByID),
		chromedp.Evaluate(`window.__gofastr.loadModule('local').then(() => Promise.all([
            window.__gofastr.local.set('local.e2e.drafts:old', {id: 'old', title: 'legacy', body: 'text from v1'}),
            window.__gofastr.local.set('local.e2e.drafts', {v: 1}),
        ]))`, nil, awaitP),
	); err != nil {
		t.Fatal(err)
	}
	openPage(t, ctx, e.srv.URL+"/")
	var rec map[string]any
	evalJSON(t, ctx, draftsJS+`.get('old')`, &rec)
	if rec["text"] != "text from v1" || rec["body"] != nil || rec["title"] != "legacy (v2)" {
		t.Fatalf("after migration get('old') = %v: want body renamed to text and the func applied", rec)
	}
	var meta map[string]any
	evalJSON(t, ctx, `window.__gofastr.local.get('local.e2e.drafts')`, &meta)
	if v, _ := meta["v"].(float64); int(v) != 2 {
		t.Fatalf("stored version = %v, want 2", meta)
	}
	var n int
	evalJSON(t, ctx, `window.__migrated`, &n)
	if n != 1 {
		t.Fatalf("the func migration ran %d times on one record", n)
	}
	var ev []map[string]any
	evalJSON(t, ctx, `window.__migrations`, &ev)
	if len(ev) != 1 || ev[0]["from"] != float64(1) || ev[0]["to"] != float64(2) {
		t.Fatalf("gofastr:local-migrated = %v", ev)
	}

	// Reload: nothing runs again.
	openPage(t, ctx, e.srv.URL+"/")
	evalJSON(t, ctx, draftsJS+`.get('old')`, &rec)
	evalJSON(t, ctx, `window.__migrated`, &n)
	if n != 0 || rec["title"] != "legacy (v2)" {
		t.Fatalf("second load: migrated %d times, title %q — a migration must run once per browser", n, rec["title"])
	}
}

// The seed bridge: SSR paints the server's default, the runtime
// patches the record in after hydration, a change to the signal
// writes back, and the value survives a client-side navigation away
// and back through the route cache.
func TestE2E_SeededSignalRoundTripsAndSurvivesNavigation(t *testing.T) {
	e := startE2E(t)
	ctx := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, ctx, e.srv.URL+"/")
	// Nothing stored yet: the default stays.
	var text string
	if err := chromedp.Run(ctx, chromedp.Text(`#seeded`, &text, chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "server default") {
		t.Fatalf("first paint = %q, want the server default", text)
	}
	// Set the signal: the record is written.
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__gofastr.setSignal('e2elocal.current', {id: 'current', title: 'typed by the user'})`, nil)); err != nil {
		t.Fatal(err)
	}
	if !pollTrue(ctx, draftsJS+`.get('current').then(v => !!v && v.title === 'typed by the user')`) {
		t.Fatal("a signal change never reached the record")
	}
	// Reload: SSR paints the default, then the record wins.
	openPage(t, ctx, e.srv.URL+"/")
	if !pollTrue(ctx, `Promise.resolve(document.getElementById('seeded').textContent.indexOf('typed by the user') >= 0)`) {
		_ = chromedp.Run(ctx, chromedp.Text(`#seeded`, &text, chromedp.ByID))
		t.Fatalf("after reload the seeded element shows %q — the record was not patched in", text)
	}
	// Navigate away and back through the runtime's router, then check
	// the restored page still shows the record and the signal.
	if err := chromedp.Run(ctx,
		chromedp.Click(`#away`, chromedp.ByID),
	); err != nil {
		t.Fatal(err)
	}
	if !pollTrue(ctx, `Promise.resolve(location.pathname === '/other')`) {
		t.Fatal("navigation to /other never happened")
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.back()`, nil)); err != nil {
		t.Fatal(err)
	}
	if !pollTrue(ctx, `Promise.resolve(location.pathname === '/' && !!document.getElementById('seeded') && document.getElementById('seeded').textContent.indexOf('typed by the user') >= 0)`) {
		_ = chromedp.Run(ctx, chromedp.Text(`#seeded`, &text, chromedp.ByID))
		t.Fatalf("after back-navigation the seeded element shows %q", text)
	}
	var sig map[string]any
	evalJSON(t, ctx, `Promise.resolve(window.__gofastr.getSignal('e2elocal.current'))`, &sig)
	if sig["title"] != "typed by the user" {
		t.Fatalf("signal after back = %v", sig)
	}
}

// The upload bridge delivers exactly the declared collection and key
// to the wrapped Go handler — and not the undeclared records — and
// the download bridge writes the handler's records back.
func TestE2E_UploadDeliversOnlyTheDeclaredAndDownloadWritesBack(t *testing.T) {
	e := startE2E(t)
	ctx := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, ctx, e.srv.URL+"/")
	evalJSON(t, ctx, `Promise.all([
        `+draftsJS+`.put({id: 'current', title: 'mine'}),
        `+draftsJS+`.put({id: 'private', title: 'never sent'}),
        `+prefsJS+`.put('theme', {theme: 'dark'}),
    ])`, nil)
	if err := chromedp.Run(ctx, chromedp.Click(`#send`, chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	if !pollTrue(ctx, `Promise.resolve((document.getElementById('result').textContent || '').indexOf('ok') >= 0)`) {
		t.Fatal("the upload RPC never answered")
	}
	seen := e.last(t)
	if seen.Body != `{"note":"hi"}` {
		t.Fatalf("the handler read body %q: the reserved field must be stripped and the form field kept", seen.Body)
	}
	if !seen.Found || seen.Current.Title != "mine" {
		t.Fatalf("Get(current) = %+v found=%v", seen.Current, seen.Found)
	}
	if seen.Theme != "dark" {
		t.Fatalf("the mirrored prefs record did not arrive: theme=%q", seen.Theme)
	}
	for _, d := range seen.Drafts {
		if d.Key == "private" {
			t.Fatal("an undeclared record was uploaded")
		}
	}
	if len(seen.Drafts) != 1 {
		t.Fatalf("List = %+v, want only the declared key", seen.Drafts)
	}
	// Download: the response wrote two records.
	if !pollTrue(ctx, draftsJS+`.get('from-server').then(v => !!v && v.title === 'pushed')`) {
		t.Fatal("the record the response pushed never landed")
	}
	var cur map[string]any
	evalJSON(t, ctx, draftsJS+`.get('current')`, &cur)
	if cur["title"] != "mine (server)" {
		t.Fatalf("the response's rewrite of current = %v", cur)
	}
	// The mirror cookie is what a screen reads at first paint.
	var cookie string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.cookie`, &cookie)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cookie, "gofastr.local.e2e.prefs.theme=") {
		t.Fatalf("no mirror cookie in %q", cookie)
	}
}

// Two real tabs: a subscriber in one hears the other's write with
// source 'tab', and its own with source 'local'.
func TestE2E_SecondTabSeesTheWrite(t *testing.T) {
	e := startE2E(t)
	tabA := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, tabA, e.srv.URL+"/")
	if err := chromedp.Run(tabA, chromedp.Evaluate(`(() => {
        window.__heard = [];
        `+draftsJS+`.subscribe((ev) => window.__heard.push(ev.source + ':' + ev.key));
        `+draftsJS+`.put({id: 'mine', title: 'a'});
    })()`, nil)); err != nil {
		t.Fatal(err)
	}
	tabB, cancelB := chromedp.NewContext(tabA)
	t.Cleanup(cancelB)
	openPage(t, tabB, e.srv.URL+"/")
	evalJSON(t, tabB, draftsJS+`.put({id: 'theirs', title: 'b'})`, nil)
	if !pollTrue(tabA, `Promise.resolve(window.__heard.indexOf('tab:theirs') >= 0 && window.__heard.indexOf('local:mine') >= 0)`) {
		var heard []string
		_ = chromedp.Run(tabA, chromedp.Evaluate(`window.__heard`, &heard))
		t.Fatalf("tab A heard %v, want both local:mine and tab:theirs", heard)
	}
	if !pollTrue(tabA, draftsJS+`.get('theirs').then(v => !!v && v.title === 'b')`) {
		t.Fatal("tab A cannot read tab B's record")
	}
}

// Logout by full navigation: ClearOnNextLoad plants the bit, the next
// page load clears every collection and drops the cookie.
func TestE2E_ClearOnNextLoad(t *testing.T) {
	e := startE2E(t)
	ctx := chromedptest.Context(t, chromedptest.Timeout(120*time.Second))
	openPage(t, ctx, e.srv.URL+"/")
	evalJSON(t, ctx, `Promise.all([`+draftsJS+`.put({id: 'x', title: 'a'}), `+prefsJS+`.put('theme', {theme: 'dark'})])`, nil)
	if err := chromedp.Run(ctx, chromedp.Navigate(e.srv.URL+"/logout"), chromedp.WaitVisible(`#ready`, chromedp.ByID)); err != nil {
		t.Fatal(err)
	}
	openPage(t, ctx, e.srv.URL+"/")
	if !pollTrue(ctx, draftsJS+`.count().then(n => n === 0)`) {
		t.Fatal("drafts survived the clear bit")
	}
	if !pollTrue(ctx, prefsJS+`.count().then(n => n === 0)`) {
		t.Fatal("prefs survived the clear bit")
	}
	var cookie string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.cookie`, &cookie)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cookie, "gofastr.local.clear.e2e=") || strings.Contains(cookie, "gofastr.local.e2e.prefs.theme=") {
		t.Fatalf("cookies after clear: %q", cookie)
	}
}
