package local

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/DonaldMurillo/gofastr/core-ui/app"
)

// Reading browser-held records on the server. Two sources, both
// explicit:
//
//   - the records an Upload.Wrap put on the request context (the
//     __local field of an RPC body), and
//   - the cookies a Mirror collection keeps, read off the request in
//     the context (uihost installs it for screens with app.WithRequest;
//     Upload.Wrap installs it for handlers).
//
// A context record wins over a cookie for the same key. Everything is
// a client hint: validated against the declaration (collection, key,
// size, JSON shape into T), never trusted. A value that fails to
// decode into T is an error, not a zero value marching on.

// The cookie namespace, one segment per record, component-encoded by
// the browser: gofastr.local.<app>.<collection>.<key>.
const cookiePrefix = "gofastr.local."

// Record is one key/value pair of a collection.
type Record[T any] struct {
	Key   string
	Value T
}

// Records is the raw view FromContext returns: every record the
// request carried for one store, by collection and key, as JSON.
type Records struct {
	app  string
	recs map[string]map[string]json.RawMessage
}

// FromContext returns every record the request carried for s: the
// uploaded ones and the mirrored cookies. Prefer the typed Get and
// List; this is the escape hatch for a handler that inspects what
// arrived.
func FromContext(ctx context.Context, s *Store) *Records {
	out := &Records{app: s.app, recs: map[string]map[string]json.RawMessage{}}
	if r := app.RequestFromContext(ctx); r != nil {
		for _, c := range r.Cookies() {
			coll, key, raw, ok := s.decodeCookie(c)
			if !ok {
				continue
			}
			if out.recs[coll] == nil {
				out.recs[coll] = map[string]json.RawMessage{}
			}
			out.recs[coll][key] = raw
		}
	}
	for coll, byKey := range recordsFrom(ctx, s.app) {
		if out.recs[coll] == nil {
			out.recs[coll] = map[string]json.RawMessage{}
		}
		for k, v := range byKey {
			out.recs[coll][k] = v
		}
	}
	return out
}

// Collections returns the collection names that carried at least one
// record, sorted.
func (r *Records) Collections() []string { return sortedKeys(r.recs) }

// Keys returns the keys that arrived for collection, sorted.
func (r *Records) Keys(collection string) []string { return sortedKeys(r.recs[collection]) }

// Raw returns the JSON of one record and whether it arrived.
func (r *Records) Raw(collection, key string) (json.RawMessage, bool) {
	v, ok := r.recs[collection][key]
	return v, ok
}

// decodeCookie recognises a mirror cookie of this store: the name is
// the namespace plus one component-encoded "<app>.<collection>.<key>",
// the value the component-encoded JSON text. Anything else — another
// app's, an undeclared or unmirrored collection, a bad key, a value
// over the record cap — is ignored.
func (s *Store) decodeCookie(c *http.Cookie) (coll, key string, raw json.RawMessage, ok bool) {
	if !strings.HasPrefix(c.Name, cookiePrefix) {
		return "", "", nil, false
	}
	name, err := url.PathUnescape(c.Name[len(cookiePrefix):])
	if err != nil || !strings.HasPrefix(name, s.app+".") {
		return "", "", nil, false
	}
	rest := name[len(s.app)+1:]
	dot := strings.IndexByte(rest, '.')
	if dot <= 0 {
		return "", "", nil, false
	}
	coll, key = rest[:dot], rest[dot+1:]
	def := s.defOf(coll)
	if def == nil || !def.mirror || !ValidKey(key) {
		return "", "", nil, false
	}
	text, err := url.PathUnescape(c.Value)
	if err != nil || len(text) > def.maxRecord || !json.Valid([]byte(text)) {
		return "", "", nil, false
	}
	return coll, key, json.RawMessage(text), true
}

// Get returns record key of c as the request carried it: from the
// upload first, then from the mirror cookie. found is false when the
// request carried nothing for the key; err reports a record that does
// not decode into T.
func Get[T any](ctx context.Context, c *Collection[T], key string) (value T, found bool, err error) {
	raw, ok := rawFor(ctx, c.def, key)
	if !ok {
		return value, false, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, true, fmt.Errorf("local: record %q/%q does not decode into %T: %w", c.def.name, key, value, err)
	}
	return value, true, nil
}

// List returns every record of c the request carried, sorted by key.
// A record that does not decode into T fails the whole list.
func List[T any](ctx context.Context, c *Collection[T]) ([]Record[T], error) {
	all := FromContext(ctx, c.def.store).recs[c.def.name]
	out := make([]Record[T], 0, len(all))
	for _, k := range sortedKeys(all) {
		var v T
		if err := json.Unmarshal(all[k], &v); err != nil {
			return nil, fmt.Errorf("local: record %q/%q does not decode into %T: %w", c.def.name, k, v, err)
		}
		out = append(out, Record[T]{Key: k, Value: v})
	}
	return out, nil
}

func rawFor(ctx context.Context, def *collectionDef, key string) (json.RawMessage, bool) {
	if v, ok := recordsFrom(ctx, def.store.app)[def.name][key]; ok {
		return v, true
	}
	if !def.mirror {
		return nil, false
	}
	r := app.RequestFromContext(ctx)
	if r == nil {
		return nil, false
	}
	for _, c := range r.Cookies() {
		coll, k, raw, ok := def.store.decodeCookie(c)
		if ok && coll == def.name && k == key {
			return raw, true
		}
	}
	return nil, false
}
