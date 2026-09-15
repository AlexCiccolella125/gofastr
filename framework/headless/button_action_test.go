package headless

import (
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/html"
)

// A button that fires a request says so through one named seam, and
// nothing else can put a request on it.
//
// The host framework's wrappers splice their attributes into the
// rendered string at the first '>'. That works, and it also means a
// request can be attached to any markup from outside, invisibly to the
// component and to whoever reads its props. Here the request is a
// prop: reviewable in one place, refused on a link, and impossible to
// smuggle in as decoration.
func TestButtonCarriesARequestOnlyThroughAction(t *testing.T) {
	req := html.Attrs{
		"data-fui-rpc":        "/apps/api/restart",
		"data-fui-rpc-method": "POST",
		"data-fui-rpc-signal": "apps",
		"data-fui-confirm":    "Restart api?",
	}
	got := Button(ButtonProps{Label: "Restart", Action: req}, nil)
	for k, v := range req {
		has(t, got, k+`="`+v+`"`, "the action's "+k+" did not land on the button")
	}

	// The same attributes through ExtraAttrs are dropped: a test id
	// must never be able to turn into a request.
	smuggled := Button(ButtonProps{Label: "Restart", ExtraAttrs: req}, nil)
	for k := range req {
		hasNot(t, smuggled, k, "a request arrived through ExtraAttrs, which is for decoration")
	}
}

func TestButtonActionAcceptsOnlyRequestAttributes(t *testing.T) {
	for _, k := range []string{"data-fui-signal", "data-fui-pane-key", "data-fui-comp", "data-fui-optimistic-endpoint", "onclick", "data-hui-copy"} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Action carrying %q was accepted; only a request belongs in this seam", k)
				} else if !strings.Contains(r.(string), k) {
					t.Errorf("the refusal for %q does not name it: %v", k, r)
				}
			}()
			Button(ButtonProps{Label: "x", Action: html.Attrs{k: "y", "data-fui-rpc": "/x"}}, nil)
		}()
	}
}

func TestButtonRefusesAnActionOnALink(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a link with a request on it was rendered; a link navigates and a button acts")
		}
	}()
	Button(ButtonProps{Label: "Go", Href: "/apps", Action: html.Attrs{"data-fui-rpc": "/x"}}, nil)
}

// An empty Action is the common case and must cost nothing: no
// attributes, no panic, the same markup as before the seam existed.
func TestButtonWithoutAnActionIsUnchanged(t *testing.T) {
	plain := Button(ButtonProps{Label: "Save"}, nil)
	withNil := Button(ButtonProps{Label: "Save", Action: nil}, nil)
	withEmpty := Button(ButtonProps{Label: "Save", Action: html.Attrs{}}, nil)
	if plain != withNil || plain != withEmpty {
		t.Errorf("an absent action changed the markup:\n%s\n%s\n%s", plain, withNil, withEmpty)
	}
	hasNot(t, plain, "data-fui", "a button with no action carries framework attributes")
}
