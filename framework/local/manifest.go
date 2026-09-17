package local

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
)

// The browser reads the declaration from window.__gofastr_local[<app>],
// which this script assigns. It is served on the host's extra-script
// rail (uihost.WithExtraScripts(store.ScriptURL()) plus the handler
// mounted at store.ScriptPath()), the rail computed reducers and
// migration functions already use, so it loads after runtime.js on
// every full shell render and never inline: the page stays CSP-clean.
// A declaration that came from the DOM instead would let markup
// planted in an island response redefine a collection's caps or
// migrations; this rail is same-origin script the app serves.

// ScriptPath is the path to mount ScriptHandler at:
// /__gofastr/local/<app>.js.
func (s *Store) ScriptPath() string { return "/__gofastr/local/" + s.app + ".js" }

// ScriptJS returns the manifest script. The first call freezes the
// declaration: a Define after it panics.
func (s *Store) ScriptJS() []byte {
	s.freeze()
	s.mu.Lock()
	defer s.mu.Unlock()
	cols := map[string]manifestEntry{}
	for n, d := range s.colls {
		cols[n] = d.manifest()
	}
	// mirrorMax travels with the declaration: the browser enforces the
	// same aggregate budget on the cookies it actually holds, which are
	// larger than the records they carry (component encoding).
	body, err := json.Marshal(map[string]any{"collections": cols, "mirrorMax": MirrorStoreMaxBytes})
	if err != nil {
		panic("local: manifest does not encode: " + err.Error())
	}
	appKey, _ := json.Marshal(s.app)
	var b bytes.Buffer
	b.WriteString("// framework/local: the browser manifest for store ")
	b.WriteString(s.app)
	b.WriteString(". Generated from the Go declaration; do not edit.\n")
	b.WriteString("window.__gofastr_local = window.__gofastr_local || {};\n")
	b.WriteString("window.__gofastr_local[")
	b.Write(appKey)
	b.WriteString("] = ")
	b.Write(body)
	b.WriteString(";\n")
	return b.Bytes()
}

// ScriptURL is ScriptPath plus ?v=<content hash>, the URL to hand
// uihost.WithExtraScripts so the script caches immutably and busts
// when the declaration changes.
func (s *Store) ScriptURL() string {
	js := s.ScriptJS()
	return s.ScriptPath() + "?v=" + scriptHash(js)
}

func scriptHash(js []byte) string {
	sum := sha256.Sum256(js)
	return hex.EncodeToString(sum[:8])
}

// ScriptRouter is the single method Serve needs: a GET route. The
// framework's *router.Router satisfies it, and so does anything else
// that mounts an http.Handler on a path.
type ScriptRouter interface {
	Get(pattern string, handler http.Handler)
}

// Serve mounts ScriptHandler at ScriptPath on rt and returns the
// ScriptURL to hand uihost.WithExtraScripts:
//
//	host := uihost.New(site, uihost.WithExtraScripts(Site.Serve(app.Router())))
//
// The route and the extra script are two halves of one thing, and doing
// only one of them fails SILENTLY: without the route the manifest 404s,
// window.__gofastr_local stays undefined, localStore(app) answers null,
// and the page script dies on a null read with nothing pointing at the
// missing line. Serve is the half that cannot be forgotten.
// ScriptPath, ScriptURL and ScriptHandler stay exported for a host that
// mounts its own routes (a subrouter, an asset CDN, a test server).
func (s *Store) Serve(rt ScriptRouter) string {
	url, mount := s.Script()
	mount(rt)
	return url
}

// Script is Serve for an app whose host is built BEFORE its router. It
// returns the same URL and the mount step, so the two halves can be
// taken in either order:
//
//	url, mount := Site.Script()
//	host := uihost.New(site, uihost.WithExtraScripts(url))
//	app := framework.New(..., host)
//	mount(app.Router())
//
// Serve asks for the router first, which is the right shape when the
// router exists — one call, and the route and the rail cannot come
// apart. But every uihost app builds the host first and gets its router
// from the app the host went into, so Serve inverted the construction
// order of the first real consumer: the subrouter had to be built three
// statements earlier than it was. Returning the mount step instead of
// demanding the router keeps both halves in one expression without
// dictating when the second one runs.
//
// The forgettable half is now visible in the signature rather than
// absent from it: an app that never calls mount serves a 404 at
// ScriptPath, and the browser says so by name
// ("no manifest for app <id> - is /__gofastr/local/<id>.js served?").
// Like Serve, the first call freezes the declaration.
func (s *Store) Script() (string, func(ScriptRouter)) {
	return s.ScriptURL(), func(rt ScriptRouter) {
		rt.Get(s.ScriptPath(), s.ScriptHandler())
	}
}

// ScriptHandler serves ScriptJS as JavaScript with a strong ETag and
// immutable caching when the request's ?v= matches the content hash
// (the policy every /__gofastr script follows). Mount it at
// ScriptPath on the app's router.
func (s *Store) ScriptHandler() http.Handler {
	var once sync.Once
	var body []byte
	var hash string
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			body = s.ScriptJS()
			hash = scriptHash(body)
		})
		h := w.Header()
		h.Set("Content-Type", "application/javascript; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		etag := `"` + hash + `"`
		h.Set("ETag", etag)
		if r.URL.Query().Get("v") == hash {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
