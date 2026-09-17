package local

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/app"
	"github.com/DonaldMurillo/gofastr/core-ui/store"
)

type draft struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Text  string   `json:"text"`
	Tags  []string `json:"tags,omitempty"`
}

type prefs struct {
	Theme string `json:"theme"`
}

// fresh gives each test its own store namespace: New panics on a
// duplicate app id by design, and the tests share one process.
func fresh(t *testing.T, app string) *Store {
	t.Helper()
	resetForTest()
	return New(app)
}

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("no panic; wanted one mentioning %q", want)
		}
		if !strings.Contains(r.(string), want) {
			t.Fatalf("panic %q does not mention %q", r, want)
		}
	}()
	fn()
}

// ─── declaration ────────────────────────────────────────────────

func TestNewValidatesAndRefusesDuplicates(t *testing.T) {
	resetForTest()
	mustPanic(t, "app id", func() { New("Site") })
	mustPanic(t, "app id", func() { New("a.b") })
	New("site")
	mustPanic(t, "already declared", func() { New("site") })
}

func TestDefineValidatesTheDeclaration(t *testing.T) {
	s := fresh(t, "site")
	mustPanic(t, "must match", func() { Define[draft](s, "Drafts", CollectionConfig{Version: 1}) })
	mustPanic(t, "Version must be 1", func() { Define[draft](s, "drafts", CollectionConfig{}) })
	mustPanic(t, "no migration to version 2", func() { Define[draft](s, "drafts", CollectionConfig{Version: 2}) })
	mustPanic(t, "outside (1, 1]", func() {
		Define[draft](s, "drafts", CollectionConfig{Version: 1, Migrations: []Migration{{Version: 2}}})
	})
	mustPanic(t, "two migrations", func() {
		Define[draft](s, "drafts", CollectionConfig{Version: 2, Migrations: []Migration{{Version: 2}, {Version: 2}}})
	})
	mustPanic(t, "zero Step", func() {
		Define[draft](s, "drafts", CollectionConfig{Version: 2, Migrations: []Migration{{Version: 2, Steps: []Step{{}}}}})
	})
	mustPanic(t, "exceeds the ceiling", func() {
		Define[draft](s, "drafts", CollectionConfig{Version: 1, MaxRecordBytes: MaxRecordBytesLimit + 1})
	})
	mustPanic(t, "exceeds MaxBytes", func() {
		Define[draft](s, "drafts", CollectionConfig{Version: 1, MaxRecordBytes: 1 << 20, MaxBytes: 1 << 19})
	})
	mustPanic(t, "does not round-trip", func() { Define[chan int](s, "chans", CollectionConfig{Version: 1}) })
	mustPanic(t, "needs an object record type", func() {
		Define[string](s, "names", CollectionConfig{Version: 1, KeyField: "id"})
	})
	Define[draft](s, "drafts", CollectionConfig{Version: 1})
	mustPanic(t, "already declared", func() { Define[draft](s, "drafts", CollectionConfig{Version: 1}) })

	// Mirror clamps to the cookie-sized defaults and ceilings.
	p := Define[prefs](s, "prefs", CollectionConfig{Version: 1, Mirror: true})
	if p.MaxRecordBytes() != MirrorDefaultMaxRecordBytes || p.MaxRecords() != MirrorDefaultMaxRecords {
		t.Fatalf("mirror caps = %d/%d, want %d/%d", p.MaxRecordBytes(), p.MaxRecords(), MirrorDefaultMaxRecordBytes, MirrorDefaultMaxRecords)
	}
	mustPanic(t, "exceeds the ceiling", func() {
		Define[prefs](s, "prefs2", CollectionConfig{Version: 1, Mirror: true, MaxRecordBytes: 4096})
	})
	// Every mirrored collection rides the Cookie header on every
	// request, so the store has one budget across all of them: a second
	// mirrored collection that fits on its own can still be refused.
	mustPanic(t, "over the 4096-byte budget", func() {
		Define[prefs](s, "prefs3", CollectionConfig{Version: 1, Mirror: true, MaxRecordBytes: 1024, MaxRecords: 16})
	})

	// The manifest freezes the declaration.
	_ = s.ScriptJS()
	mustPanic(t, "after store", func() { Define[draft](s, "late", CollectionConfig{Version: 1}) })
}

func TestManifestCarriesTheDeclaration(t *testing.T) {
	s := fresh(t, "site")
	Define[draft](s, "drafts", CollectionConfig{
		Version: 3, KeyField: "id", MaxRecordBytes: 1024, MaxRecords: 5, MaxBytes: 4096,
		Migrations: []Migration{
			{Version: 2, Steps: []Step{Rename("body", "text"), Default("tags", []string{})}},
			{Version: 3, Steps: []Step{Remove("legacy"), Func("drafts-v3")}},
		},
	})
	Define[prefs](s, "prefs", CollectionConfig{Version: 1, Mirror: true})
	js := string(s.ScriptJS())
	if !strings.HasPrefix(js, "// framework/local") || !strings.Contains(js, `window.__gofastr_local["site"] = {`) {
		t.Fatalf("manifest script shape:\n%s", js)
	}
	body := js[strings.Index(js, "] = ")+4 : len(js)-2]
	var m struct {
		Collections map[string]struct {
			V          int    `json:"v"`
			Key        string `json:"key"`
			MaxRecord  int    `json:"maxRecord"`
			MaxRecords int    `json:"maxRecords"`
			MaxBytes   int    `json:"maxBytes"`
			Mirror     bool   `json:"mirror"`
			Migrations []struct {
				V     int              `json:"v"`
				Steps []map[string]any `json:"steps"`
			} `json:"migrations"`
		} `json:"collections"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("manifest is not JSON: %v\n%s", err, body)
	}
	d := m.Collections["drafts"]
	if d.V != 3 || d.Key != "id" || d.MaxRecord != 1024 || d.MaxRecords != 5 || d.MaxBytes != 4096 || d.Mirror {
		t.Fatalf("drafts entry = %+v", d)
	}
	if len(d.Migrations) != 2 || d.Migrations[0].V != 2 || len(d.Migrations[0].Steps) != 2 || d.Migrations[1].Steps[1]["op"] != "func" || d.Migrations[1].Steps[1]["name"] != "drafts-v3" {
		t.Fatalf("migrations = %+v", d.Migrations)
	}
	if d.Migrations[0].Steps[0]["op"] != "rename" || d.Migrations[0].Steps[0]["from"] != "body" || d.Migrations[0].Steps[0]["to"] != "text" {
		t.Fatalf("rename step = %v", d.Migrations[0].Steps[0])
	}
	p := m.Collections["prefs"]
	if !p.Mirror || p.MaxRecord != MirrorDefaultMaxRecordBytes || len(p.Migrations) != 0 {
		t.Fatalf("prefs entry = %+v", p)
	}
	if !strings.HasPrefix(s.ScriptURL(), s.ScriptPath()+"?v=") || s.ScriptPath() != "/__gofastr/local/site.js" {
		t.Fatalf("ScriptURL = %q", s.ScriptURL())
	}

	// The handler serves it immutably when the hash matches.
	rec := httptest.NewRecorder()
	s.ScriptHandler().ServeHTTP(rec, httptest.NewRequest("GET", s.ScriptURL(), nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("script handler: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	s.ScriptHandler().ServeHTTP(rec, httptest.NewRequest("GET", s.ScriptPath(), nil))
	if strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("an unversioned fetch must not cache immutably")
	}
}

func TestKeyOfReadsTheDeclaredField(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1, KeyField: "id"})
	k, err := d.KeyOf(draft{ID: "abc"})
	if err != nil || k != "abc" {
		t.Fatalf("KeyOf = %q, %v", k, err)
	}
	if _, err := d.KeyOf(draft{}); err == nil {
		t.Fatal("an empty key field must not be a key")
	}
	n := Define[draft](s, "nokey", CollectionConfig{Version: 1})
	if _, err := n.KeyOf(draft{ID: "x"}); err == nil {
		t.Fatal("KeyOf on a collection with no KeyField must error")
	}
	mustPanic(t, "not a valid key", func() { d.Key("") })
	mustPanic(t, "not a valid key", func() { d.Key("__proto__") })
}

// ─── seed ───────────────────────────────────────────────────────

func TestSeedSignalBindsWithMarkersAndGoesGlobal(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1})
	sl := store.JSON[draft](store.New("seedtest"), "current", draft{Title: "default"})
	seed := SeedSignal(d, "current", sl)
	if sl.Scope() != store.ScopeGlobal {
		t.Fatal("SeedSignal must imply Global: the browser's value has to survive a client navigation")
	}
	html := string(seed.Bind(context.Background(), "p", map[string]string{"class": "x"}))
	for _, want := range []string{`data-local-store="site"`, `data-local-seed="drafts:current"`, `data-fui-signal="seedtest.current"`, `class="x"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("Bind lacks %s:\n%s", want, html)
		}
	}
	mustPanic(t, "not a valid key", func() { SeedSignal(d, "", sl) })
	persisted := store.JSON[draft](store.New("seedtest"), "persisted", draft{}).Persist()
	mustPanic(t, "one owner", func() { SeedSignal(d, "k", persisted) })
}

// ─── send / upload ──────────────────────────────────────────────

func TestSendAttrsNameOnlyTheDeclaration(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1})
	p := Define[prefs](s, "prefs", CollectionConfig{Version: 1, Mirror: true})
	u := Send(d.Key("current"), p)
	a := u.Attrs()
	if a["data-local-store"] != "site" || a["data-local-send"] != "drafts:current,prefs" || a["data-fui-rpc-with"] != BridgeName {
		t.Fatalf("Attrs = %v", a)
	}
	m := u.Merge(map[string]string{"data-fui-rpc": "/x", "class": "c"})
	if m["data-fui-rpc"] != "/x" || m["class"] != "c" || m["data-local-send"] == "" {
		t.Fatalf("Merge = %v", m)
	}
	mustPanic(t, "GET", func() { u.Merge(map[string]string{"data-fui-rpc-method": "get"}) })
	mustPanic(t, "at least one", func() { Send() })
	other := fresh(t, "other")
	od := Define[draft](other, "drafts", CollectionConfig{Version: 1})
	mustPanic(t, "one store per upload", func() { Send(d, od) })
	mustPanic(t, "outside", func() { Send(d).Max(0) })
}

// echo is the wrapped handler under test: it records what it saw.
type echo struct {
	body    string
	form    url.Values
	drafts  []Record[draft]
	current draft
	found   bool
	err     error
	req     *http.Request
}

func wrapEcho(t *testing.T, u *Upload, d *Collection[draft]) (*echo, http.Handler) {
	t.Helper()
	e := &echo{}
	return e, u.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.req = app.RequestFromContext(r.Context())
		b, _ := io.ReadAll(r.Body)
		e.body = string(b)
		if r.Form != nil {
			e.form = r.PostForm
		}
		e.drafts, e.err = List(r.Context(), d)
		e.current, e.found, _ = Get(r.Context(), d, "current")
		w.WriteHeader(204)
	})
}

func TestWrapLiftsTheReservedFieldOutOfAJSONBody(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1})
	e, h := wrapEcho(t, Send(d), d)
	body := `{"note":"hi","__local":{"drafts":[{"k":"current","v":{"id":"current","title":"T","text":"body"}},{"k":"b","v":{"id":"b","title":"B"}}]}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/up", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e.body != `{"note":"hi"}` {
		t.Fatalf("the handler saw %q, want the body without the reserved field", e.body)
	}
	if e.req == nil {
		t.Fatal("Wrap must install app.WithRequest so cookie reads work in the handler")
	}
	if !e.found || e.current.Title != "T" || e.current.Text != "body" {
		t.Fatalf("Get(current) = %+v found=%v", e.current, e.found)
	}
	if e.err != nil || len(e.drafts) != 2 || e.drafts[0].Key != "b" || e.drafts[1].Key != "current" {
		t.Fatalf("List = %+v, %v (want two, sorted by key)", e.drafts, e.err)
	}
	raw := FromContext(context.Background(), s)
	if len(raw.Collections()) != 0 {
		t.Fatal("a bare context carries nothing")
	}
}

func TestWrapPassesABodyWithoutTheFieldUntouched(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1})
	e, h := wrapEcho(t, Send(d), d)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/up", strings.NewReader(`{"b":1,"a":2}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != 204 || e.body != `{"b":1,"a":2}` || e.found || len(e.drafts) != 0 {
		t.Fatalf("status %d body %q found %v drafts %v — a body with no reserved field must reach the handler byte for byte", rec.Code, e.body, e.found, e.drafts)
	}
	// An empty body, and a non-JSON content type, pass through too.
	for _, ct := range []string{"application/json", "text/plain"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("POST", "/up", strings.NewReader(""))
		req.Header.Set("Content-Type", ct)
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("%s empty body: %d", ct, rec.Code)
		}
	}
}

func TestWrapRefusesWhatWasNotDeclared(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1, MaxRecordBytes: 64, MaxRecords: 2, MaxBytes: 100})
	Define[prefs](s, "prefs", CollectionConfig{Version: 1})
	_, h := wrapEcho(t, Send(d.Key("current")), d)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/up", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}
	cases := []struct {
		name, body string
		want       int
	}{
		{"declared key", `{"__local":{"drafts":[{"k":"current","v":{"id":"current"}}]}}`, 204},
		{"undeclared key of a declared collection", `{"__local":{"drafts":[{"k":"other","v":{}}]}}`, 400},
		{"declared store, undeclared collection", `{"__local":{"prefs":[{"k":"theme","v":"dark"}]}}`, 400},
		{"unknown collection", `{"__local":{"nope":[{"k":"x","v":1}]}}`, 400},
		{"bad key", `{"__local":{"drafts":[{"k":"__proto__","v":1}]}}`, 400},
		{"record over the cap", `{"__local":{"drafts":[{"k":"current","v":"` + strings.Repeat("x", 70) + `"}]}}`, 413},
		{"duplicate key in the body", `{"__local":{"drafts":[{"k":"current","v":1},{"k":"current","v":2}]}}`, 400},
		{"not the shape", `{"__local":[1,2]}`, 400},
		{"duplicate top-level key", `{"a":1,"A":2,"__local":{}}`, 400},
		{"missing value", `{"__local":{"drafts":[{"k":"current"}]}}`, 400},
	}
	for _, c := range cases {
		if got := post(c.body); got.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, got.Code, c.want, strings.TrimSpace(got.Body.String()))
		}
	}
	// The whole-body cap is a 413 too.
	e2, h2 := wrapEcho(t, Send(d).Max(64), d)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/up", strings.NewReader(`{"pad":"`+strings.Repeat("y", 200)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	h2.ServeHTTP(rec, req)
	if rec.Code != 413 || e2.req != nil {
		t.Fatalf("over Max: %d (handler ran: %v)", rec.Code, e2.req != nil)
	}
	// Collection caps: three records where two are allowed.
	_, h3 := wrapEcho(t, Send(d), d)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/up", strings.NewReader(`{"__local":{"drafts":[{"k":"a","v":1},{"k":"b","v":2},{"k":"c","v":3}]}}`))
	req.Header.Set("Content-Type", "application/json")
	h3.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("over MaxRecords: %d", rec.Code)
	}
}

func TestWrapLiftsTheFieldOutOfFormBodies(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1})
	e, h := wrapEcho(t, Send(d), d)

	form := url.Values{"note": {"hi"}, "__local": {`{"drafts":[{"k":"current","v":{"id":"current","title":"F"}}]}`}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/up", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != 204 || !e.found || e.current.Title != "F" {
		t.Fatalf("urlencoded: %d found=%v %+v", rec.Code, e.found, e.current)
	}
	if e.form.Get("note") != "hi" || e.form.Has("__local") {
		t.Fatalf("the handler's form = %v, want note kept and the reserved field gone", e.form)
	}

	var mb strings.Builder
	mw := multipart.NewWriter(&mb)
	_ = mw.WriteField("note", "hi")
	_ = mw.WriteField("__local", `{"drafts":[{"k":"current","v":{"id":"current","title":"M"}}]}`)
	_ = mw.Close()
	e2, h2 := wrapEcho(t, Send(d), d)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/up", strings.NewReader(mb.String()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h2.ServeHTTP(rec, req)
	if rec.Code != 204 || !e2.found || e2.current.Title != "M" || e2.form.Has("__local") || e2.form.Get("note") != "hi" {
		t.Fatalf("multipart: %d found=%v %+v form=%v", rec.Code, e2.found, e2.current, e2.form)
	}
}

// ─── mirror cookies ─────────────────────────────────────────────

func TestGetReadsAMirrorCookieAndIgnoresTheRest(t *testing.T) {
	s := fresh(t, "site")
	p := Define[prefs](s, "prefs", CollectionConfig{Version: 1, Mirror: true})
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1})
	req := httptest.NewRequest("GET", "/", nil)
	// The browser's spelling: gofastr.local. + encodeURIComponent(app.coll.key) = encodeURIComponent(JSON).
	req.AddCookie(&http.Cookie{Name: "gofastr.local.site.prefs.theme", Value: url.PathEscape(`{"theme":"dark"}`)})
	req.AddCookie(&http.Cookie{Name: "gofastr.local.site.drafts.current", Value: url.PathEscape(`{"title":"no"}`)}) // not mirrored
	req.AddCookie(&http.Cookie{Name: "gofastr.local.other.prefs.theme", Value: url.PathEscape(`{"theme":"x"}`)})    // another app
	req.AddCookie(&http.Cookie{Name: "gofastr.local.site.prefs.bad", Value: "%7Bnot-json"})                         // malformed
	req.AddCookie(&http.Cookie{Name: "gofastr.local.site.prefs.big", Value: url.PathEscape(`"` + strings.Repeat("z", 2000) + `"`)})
	ctx := app.WithRequest(context.Background(), req)

	v, found, err := Get(ctx, p, "theme")
	if err != nil || !found || v.Theme != "dark" {
		t.Fatalf("Get(theme) = %+v %v %v", v, found, err)
	}
	if _, found, _ := Get(ctx, d, "current"); found {
		t.Fatal("an unmirrored collection must never be read from a cookie")
	}
	all := FromContext(ctx, s)
	if cols := all.Collections(); len(cols) != 1 || cols[0] != "prefs" || len(all.Keys("prefs")) != 1 {
		t.Fatalf("FromContext saw %v / %v — malformed, oversized, foreign and unmirrored cookies must all be ignored", cols, all.Keys("prefs"))
	}
	// A record that does not decode into T is an error, not a zero value.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: "gofastr.local.site.prefs.theme", Value: url.PathEscape(`[1,2]`)})
	if _, found, err := Get(app.WithRequest(context.Background(), req2), p, "theme"); !found || err == nil {
		t.Fatalf("a mis-shaped record must surface as an error: found=%v err=%v", found, err)
	}
	// No request on the context: nothing, no panic.
	if _, found, _ := Get(context.Background(), p, "theme"); found {
		t.Fatal("no request, no record")
	}
	// An upload wins over the cookie for the same key.
	up := Send(p)
	var got prefs
	h := up.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got, _, _ = Get(r.Context(), p, "theme") })
	req3 := httptest.NewRequest("POST", "/", strings.NewReader(`{"__local":{"prefs":[{"k":"theme","v":{"theme":"light"}}]}}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.AddCookie(&http.Cookie{Name: "gofastr.local.site.prefs.theme", Value: url.PathEscape(`{"theme":"dark"}`)})
	h.ServeHTTP(httptest.NewRecorder(), req3)
	if got.Theme != "light" {
		t.Fatalf("upload must win over the cookie: got %+v", got)
	}
}

// ─── response header ────────────────────────────────────────────

func TestPutDeleteClearAccumulateOneASCIIHeader(t *testing.T) {
	s := fresh(t, "site")
	d := Define[draft](s, "drafts", CollectionConfig{Version: 1, MaxRecordBytes: 64})
	rec := httptest.NewRecorder()
	if err := Put(rec, d, "current", draft{ID: "current", Title: "héllo 🙂"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(rec, d, "old"); err != nil {
		t.Fatal(err)
	}
	if err := Clear(rec, s); err != nil {
		t.Fatal(err)
	}
	h := rec.Header().Get(ResponseHeader)
	for i := 0; i < len(h); i++ {
		if h[i] < 0x20 || h[i] > 0x7e {
			t.Fatalf("header byte %d is %#x: the header must be printable ASCII", i, h[i])
		}
	}
	var msg responseMsg
	if err := json.Unmarshal([]byte(h), &msg); err != nil {
		t.Fatalf("header is not JSON: %v\n%s", err, h)
	}
	if msg.App != "site" || len(msg.Ops) != 3 || msg.Ops[0].C != "drafts" || msg.Ops[0].K != "current" || !msg.Ops[1].D || !msg.Ops[2].Clear {
		t.Fatalf("ops = %+v", msg.Ops)
	}
	var v draft
	if err := json.Unmarshal(msg.Ops[0].V, &v); err != nil || v.Title != "héllo 🙂" {
		t.Fatalf("the escaped value must decode back to the rune: %+v %v", v, err)
	}
	if err := Put(rec, d, "", draft{}); !errors.Is(err, ErrBadKey) {
		t.Fatalf("bad key: %v", err)
	}
	if err := Put(rec, d, "big", draft{Text: strings.Repeat("x", 100)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the record cap: %v", err)
	}
	other := fresh(t, "other")
	if err := Clear(rec, other); err == nil {
		t.Fatal("one store per response header")
	}
	rec.Header().Set(ResponseHeader, "junk")
	if err := Clear(rec, s); err == nil {
		t.Fatal("a foreign header value must not be overwritten silently")
	}
	// The accumulated header has a cap.
	rec2 := httptest.NewRecorder()
	big := Define[draft](s, "big", CollectionConfig{Version: 1, MaxRecordBytes: 8 << 10})
	var last error
	for i := 0; i < 10; i++ {
		last = Put(rec2, big, "k", draft{Text: strings.Repeat("y", 7000)})
		if last != nil {
			break
		}
	}
	if !errors.Is(last, ErrTooLarge) {
		t.Fatalf("the header must refuse past %d bytes: %v", ResponseHeaderMaxBytes, last)
	}
}

func TestClearOnNextLoadPlantsAScriptReadableBit(t *testing.T) {
	s := fresh(t, "site")
	rec := httptest.NewRecorder()
	ClearOnNextLoad(rec, httptest.NewRequest("POST", "/logout", nil), s)
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "gofastr.local.clear.site" || cs[0].Value != "1" || cs[0].HttpOnly || cs[0].Secure || cs[0].SameSite != http.SameSiteLaxMode || cs[0].MaxAge != 86400 {
		t.Fatalf("cookie = %+v", cs)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	ClearOnNextLoad(rec, req, s)
	if !rec.Result().Cookies()[0].Secure {
		t.Fatal("Secure must follow the request scheme")
	}
}

func TestASCIIJSON(t *testing.T) {
	got := asciiJSON([]byte(`{"a":"é🙂"}`))
	if got != `{"a":"\u00e9\ud83d\ude42"}` {
		t.Fatalf("asciiJSON = %s", got)
	}
}
