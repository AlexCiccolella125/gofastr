package headless

import (
	"testing"
)

// labIsland is the island the fixtures name. A fixture render is not
// wired to a handler; it only has to satisfy the required-Island
// contract with a plausible endpoint and signal.
var labIsland = Island{Endpoint: "/island/apps", Signal: "apps"}

// The attrs function's shape: the four keys of the RPC contract, the
// query shared between the href and the endpoint, and push-state only
// on a GET that has a URL to write.
func TestIslandAttrsShape(t *testing.T) {
	isle := Island{Endpoint: "/island/apps", Signal: "apps"}
	read := isle.attrs("/apps?page=3&sort=name", "GET")
	want := map[string]string{
		"data-fui-rpc":        "/island/apps?page=3&sort=name",
		"data-fui-rpc-method": "GET",
		"data-fui-rpc-signal": "apps",
		"data-fui-push-state": "/apps?page=3&sort=name",
	}
	for k, v := range want {
		if read[k] != v {
			t.Errorf("%s = %q, want %q", k, read[k], v)
		}
	}
	if len(read) != len(want) {
		t.Errorf("a GET with an href carries more than the contract: %v", read)
	}

	// An endpoint with its own query joins rather than stacks one.
	joined := Island{Endpoint: "/island/apps?keep=1", Signal: "apps"}.
		attrs("/apps?page=2", "GET")
	if joined["data-fui-rpc"] != "/island/apps?keep=1&page=2" {
		t.Errorf("query join = %q", joined["data-fui-rpc"])
	}

	// A mutation writes no URL: where the change lands is the server's
	// to say through X-Gofastr-Push-State.
	post := isle.attrs("/apps/blog", "POST")
	if _, ok := post["data-fui-push-state"]; ok {
		t.Error("a POST carried push-state")
	}
	if post["data-fui-rpc"] != "/island/apps" {
		t.Errorf("a mutation with no verb to carry kept the href's query: %q", post["data-fui-rpc"])
	}

	// A form's trigger has no href of its own: no query, no push-state.
	form := isle.attrs("", "GET")
	if _, ok := form["data-fui-push-state"]; ok {
		t.Error("a form trigger without an href carried push-state")
	}
	if form["data-fui-rpc"] != "/island/apps" {
		t.Errorf("form rpc = %q", form["data-fui-rpc"])
	}

	// A lower-case method is canonicalised, not copied: the runtime
	// upper-cases what it reads and the markup should say it once.
	if m := isle.attrs("", "post")["data-fui-rpc-method"]; m != "POST" {
		t.Errorf("method = %q, want POST", m)
	}
}

// An island that looks wired and is not is refused at render, where
// the mistake is a panic naming it, rather than in the browser, where
// it is a region that never updates.
func TestIslandRefusesTheUnwired(t *testing.T) {
	refuse(t, "Endpoint", func() { Island{Signal: "apps"}.attrs("", "GET") })
	refuse(t, "Endpoint", func() { Island{Endpoint: "https://evil.example/x", Signal: "apps"}.attrs("", "GET") })
	refuse(t, "Endpoint", func() { Island{Endpoint: "//evil.example/x", Signal: "apps"}.attrs("", "GET") })
	refuse(t, "Signal", func() { Island{Endpoint: "/island/apps"}.attrs("", "GET") })
}

// The contract lands on the element that keeps the href or the action,
// so the no-script path and the island are one element — and the href
// or action is still there for no script.
func TestPaginationCarriesTheContractOnItsAnchors(t *testing.T) {
	got := Pagination(PaginationProps{Page: 5, Pages: 5, HrefPattern: "/apps?page=%d",
		AriaLabel: "Pages", Island: labIsland}, nil)
	for _, want := range []string{
		`href="/apps?page=4"`,
		`data-fui-rpc="/island/apps?page=4"`,
		`data-fui-rpc-method="GET"`,
		`data-fui-rpc-signal="apps"`,
		`data-fui-push-state="/apps?page=4"`,
	} {
		has(t, got, want, "the page anchor did not carry both destinations")
	}
	// The disabled end carries no contract: it goes nowhere. Attributes
	// render sorted, so a disabled anchor would show the pair.
	has(t, got, `aria-disabled="true"`, "the last page's Next is not disabled")
	hasNot(t, got, `aria-disabled="true" data-fui-rpc`, "the disabled Next carried a contract")
}

func TestToolbarSearchIsTheFormThatCarriesTheContract(t *testing.T) {
	got := ToolbarSearch(ToolbarSearchProps{Island: labIsland}, nil,
		Input(InputProps{Type: "search", Name: "q", AriaLabel: "Search apps"}, nil))
	has(t, got, `<form data-fui-rpc="/island/apps" data-fui-rpc-method="GET" data-fui-rpc-signal="apps" method="get">`,
		"the search wrapper is not the GET form carrying the contract")
	hasNot(t, got, "data-fui-push-state", "the search form wrote a URL only the server can name")
}

// A caller cannot forge or override the contract through ExtraAttrs:
// Safe drops every data-fui-* key, so the only way in is the Island.
func TestTheContractCannotBeSmuggled(t *testing.T) {
	smuggled := Pagination(PaginationProps{Page: 2, Pages: 5, HrefPattern: "/x?p=%d",
		AriaLabel: "Pages", Island: labIsland,
		ExtraAttrs: map[string]string{"data-fui-rpc": "/evil"}}, nil)
	hasNot(t, smuggled, "/evil", "a request arrived through ExtraAttrs, which is for decoration")
	has(t, smuggled, `data-fui-rpc="/island/apps?p=3"`, "the island's own contract was not rendered")
}

// A component whose whole purpose is an in-page state change refuses
// the link-only render the framework's first hard rule forbids: the
// Island is required, so that render cannot be built. A partially-set
// island is not a link-only render but a broken one, and is refused by
// its own rule.
func TestRequiredIslandsRefuseTheLinkOnlyRender(t *testing.T) {
	refuse(t, "Island", func() {
		Pagination(PaginationProps{Page: 2, Pages: 5, HrefPattern: "/x?p=%d", AriaLabel: "Pages"}, nil)
	})
	refuse(t, "Island", func() {
		ToolbarSearch(ToolbarSearchProps{}, nil, Input(InputProps{Name: "q", AriaLabel: "q"}, nil))
	})
	refuse(t, "Signal", func() {
		Pagination(PaginationProps{Page: 2, Pages: 5, HrefPattern: "/x?p=%d", AriaLabel: "Pages",
			Island: Island{Endpoint: "/island/apps"}}, nil)
	})
}

// A form is a page of its own without an Island and an island with
// one: the optional seam leaves the plain render alone and, when set,
// puts the POST contract on the form that keeps its action.
func TestOptionalIslandsAreOptional(t *testing.T) {
	plain := Form(FormProps{Action: "/apps"}, nil)
	hasNot(t, plain, "data-fui", "a form with no Island carries framework attributes")
	isled := Form(FormProps{Action: "/apps", Island: labIsland}, nil)
	has(t, isled, `action="/apps"`, "the form lost its action")
	has(t, isled, `data-fui-rpc="/island/apps" data-fui-rpc-method="POST" data-fui-rpc-signal="apps"`,
		"the form did not carry the POST contract")
}
