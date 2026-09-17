package local

import (
	"context"
	"fmt"
	"maps"

	"github.com/DonaldMurillo/gofastr/core-ui/store"
	"github.com/DonaldMurillo/gofastr/core/render"
)

// SeededSignal joins one record to one core-ui/store slice. The server
// renders the slice's value (its default, or what Seed put on the
// request); after hydration the runtime reads the record and patches
// the signal in place, then writes every later value of the signal
// back to the record and mirrors another tab's write in. The marker
// rides on the bindings, so a page that never binds the slice never
// restores it — the same rule store.Persist keeps.
//
// The record cannot appear at FIRST PAINT: IndexedDB is asynchronous by
// construction. A screen that must not flash the default needs the
// value on the request — a Mirror collection read with Get — not a
// seeded signal.
type SeededSignal[T any] struct {
	coll  *Collection[T]
	key   string
	slice *store.Slice[T]
}

// SeedSignal declares that slice is filled from record key of c. It
// panics on an invalid key and on a slice that is already browser-
// persisted through store.Persist (two owners of one value), and
// marks the slice app-global: the browser's value has to survive a
// client-side navigation, and the app-global merge rule is what keeps
// a partial render from clobbering it.
func SeedSignal[T any](c *Collection[T], key string, slice *store.Slice[T]) *SeededSignal[T] {
	if !validRecordKey(key) {
		panic(fmt.Sprintf("local: SeedSignal on %q: %q is not a valid key", c.def.name, key))
	}
	if slice.Persisted() {
		panic(fmt.Sprintf("local: SeedSignal on %q: slice %q is already store.Persist-ed — one owner per browser value", c.def.name, slice.Name()))
	}
	slice.Global()
	return &SeededSignal[T]{coll: c, key: key, slice: slice}
}

// boundSlice returns the bound slice. Unexported: the caller passed the
// slice in and still holds it; handing it back was a second name for
// something nothing asked for.
func (s *SeededSignal[T]) boundSlice() *store.Slice[T] { return s.slice }

// Name is the signal key the browser knows the slice by — the
// fully-qualified core-ui/store name, "<store>.<slice>". A page script
// WRITES a seeded signal with __gofastr.setSignal(name, value), and the
// seed bridge writes that value on to the record; without this the
// script had to hardcode a string Go owns, which is the kind of
// duplication that survives the rename it should not have survived.
// The same name is on every binding as data-fui-signal, so a script can
// read it off the DOM instead of being handed it.
func (s *SeededSignal[T]) Name() string { return s.slice.Name() }

// Attrs returns the two marker attributes a binding carries:
// data-local-store="<app>" and data-local-seed="<collection>:<key>".
// The signal's own name is not among them: store.Slice.Bind already
// puts it on the same element as data-fui-signal, and a second spelling
// of one name is a second thing to keep in step. Name() is the Go-side
// reader.
func (s *SeededSignal[T]) Attrs() map[string]string {
	return map[string]string{
		"data-local-store": s.coll.def.store.app,
		"data-local-seed":  s.coll.def.name + ":" + s.key,
	}
}

func (s *SeededSignal[T]) withMarkers(attrs map[string]string) map[string]string {
	out := make(map[string]string, len(attrs)+2)
	maps.Copy(out, attrs)
	maps.Copy(out, s.Attrs())
	return out
}

// Bind renders the slice's text binding with the seed markers on it.
func (s *SeededSignal[T]) Bind(ctx context.Context, tag string, attrs map[string]string) render.HTML {
	return s.slice.Bind(ctx, tag, s.withMarkers(attrs))
}

// BindAttr renders the slice's attribute binding with the seed markers.
func (s *SeededSignal[T]) BindAttr(ctx context.Context, tag, htmlAttr string, attrs map[string]string) render.HTML {
	return s.slice.BindAttr(ctx, tag, htmlAttr, s.withMarkers(attrs))
}

// SeededCount joins a collection's SIZE to a core-ui/store slice: the
// signal carries count(), kept live by the collection's own subscribe,
// so a screen can say "3 teams" without the app keeping a summary
// record beside the records and hoping the two stay in step. That
// denormalised record is the thing an app reaches for when the only
// bridge is one record wide, and it is the thing that drifts.
//
// It is a SCALAR and one way. A store.Slice renders its value as text,
// so the honest seed is a number the screen can paint; a list seed —
// the newest three, a name per row — is out of scope, because rendering
// a slice of records needs a template on the browser side and this
// package does not put one there. A page that needs the rows uploads
// them (Send) or reads them from its own script.
//
// Nothing is written back: the count belongs to the records. A script
// that sets the signal by hand only changes what the screen says until
// the next write to the collection corrects it.
type SeededCount[T any] struct {
	coll  *Collection[T]
	slice *store.Slice[int]
}

// SeedCount declares that slice carries the number of records in c. It
// panics on a slice that is already browser-persisted through
// store.Persist, and marks the slice app-global for the same reason
// SeedSignal does: the browser's value has to survive a client-side
// navigation.
//
// Like every seed, it lands AFTER hydration: the server renders the
// slice's default (0, or a server-side estimate) and the runtime
// patches the real count in. A count at first paint means a Mirror
// collection, and a mirrored collection is a few tiny records.
func SeedCount[T any](c *Collection[T], slice *store.Slice[int]) *SeededCount[T] {
	if slice.Persisted() {
		panic(fmt.Sprintf("local: SeedCount on %q: slice %q is already store.Persist-ed — one owner per browser value", c.def.name, slice.Name()))
	}
	slice.Global()
	return &SeededCount[T]{coll: c, slice: slice}
}

// Name is the signal key the browser knows the slice by, the
// fully-qualified core-ui/store name. Read it rather than retyping it
// in a page script.
func (s *SeededCount[T]) Name() string { return s.slice.Name() }

// Attrs returns the two marker attributes a binding carries:
// data-local-store="<app>" and data-local-count="<collection>".
func (s *SeededCount[T]) Attrs() map[string]string {
	return map[string]string{
		"data-local-store": s.coll.def.store.app,
		"data-local-count": s.coll.def.name,
	}
}

func (s *SeededCount[T]) withMarkers(attrs map[string]string) map[string]string {
	out := make(map[string]string, len(attrs)+2)
	maps.Copy(out, attrs)
	maps.Copy(out, s.Attrs())
	return out
}

// Bind renders the slice's text binding with the count markers on it.
func (s *SeededCount[T]) Bind(ctx context.Context, tag string, attrs map[string]string) render.HTML {
	return s.slice.Bind(ctx, tag, s.withMarkers(attrs))
}

// BindAttr renders the slice's attribute binding with the count markers.
func (s *SeededCount[T]) BindAttr(ctx context.Context, tag, htmlAttr string, attrs map[string]string) render.HTML {
	return s.slice.BindAttr(ctx, tag, htmlAttr, s.withMarkers(attrs))
}
