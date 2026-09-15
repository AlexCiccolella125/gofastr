package headless

import (
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core/render"
)

// A binding is a seam of its own because an override may not carry a
// data-fui-* key. The binding lands on the named part, on that part
// only, and renders exactly the triple the framework's runtime reads.
func TestBindLandsOnTheNamedPartOnly(t *testing.T) {
	got := Card(CardProps{Title: "CPU", Seams: Seams{Binds: Binds{
		PartCardBody: {Signal: "cpu"},
	}}}, nil, render.HTML("<p>41%</p>"))
	has(t, got, `data-fui-signal="cpu"`, "the binding never arrived")
	has(t, got, `data-fui-signal-mode="text"`, "text is the default mode and must be stated for the runtime")
	if n := count(got, "data-fui-signal="); n != 1 {
		t.Errorf("the binding landed %d times; it must land on the one part that asked", n)
	}
	hasNot(t, got, "data-fui-signal-attr", "a text binding carries an attribute name")
}

func TestBindAttrModeNamesItsAttribute(t *testing.T) {
	got := Card(CardProps{Title: "CPU", Seams: Seams{Binds: Binds{
		PartCardBody: {Signal: "busy", Mode: "attr", Attr: "aria-busy"},
	}}}, nil, render.HTML("<p>x</p>"))
	has(t, got, `data-fui-signal-mode="attr"`, "attr mode is not stated")
	has(t, got, `data-fui-signal-attr="aria-busy"`, "the attribute to write is missing")
}

// The framework's runtime executes some attributes regardless of the
// value bound to them, so its allow-list is the rule here too, at
// render, as a panic with a reason. A reserved signal name is refused
// for the same reason: the runtime would never write it, and the part
// would sit still forever.
func TestBindRefusesWhatTheRuntimeWouldRefuse(t *testing.T) {
	for _, c := range []struct {
		name string
		bind Bind
		want string
	}{
		{"no signal", Bind{}, "needs a Signal"},
		{"reserved name", Bind{Signal: "__proto__"}, "reserved"},
		{"unknown mode", Bind{Signal: "s", Mode: "innerText"}, "must be text, html or attr"},
		{"attr without name", Bind{Signal: "s", Mode: "attr"}, "needs the Attr"},
		{"text with attr", Bind{Signal: "s", Attr: "title"}, "takes no Attr"},
		{"executable attr", Bind{Signal: "s", Mode: "attr", Attr: "onclick"}, "may not write onclick"},
		{"style attr", Bind{Signal: "s", Mode: "attr", Attr: "style"}, "may not write style"},
		{"privileged attr", Bind{Signal: "s", Mode: "attr", Attr: "data-fui-rpc"}, "may not write data-fui-rpc"},
	} {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Errorf("%s: rendered instead of refusing", c.name)
					return
				}
				if !strings.Contains(r.(string), c.want) {
					t.Errorf("%s: refused for the wrong reason: %v", c.name, r)
				}
			}()
			Card(CardProps{Title: "x", Seams: Seams{Binds: Binds{PartCardBody: c.bind}}}, nil, render.HTML("<p>x</p>"))
		}()
	}
}

// Local mutations are what a click does, so they travel through the
// Action seam like a request does, and nowhere else.
func TestButtonActionAdmitsLocalSignalMutations(t *testing.T) {
	got := Button(ButtonProps{Label: "+", Action: map[string]string{"data-fui-signal-inc": "count:5"}}, nil)
	has(t, got, `data-fui-signal-inc="count:5"`, "the local mutation never arrived")
	smuggled := Button(ButtonProps{Label: "+", ExtraAttrs: map[string]string{"data-fui-signal-inc": "count"}}, nil)
	hasNot(t, smuggled, "data-fui-signal-inc", "a local mutation arrived through ExtraAttrs")
	defer func() {
		if recover() == nil {
			t.Error("a signal BINDING was accepted as an action; a binding is a seam of its own")
		}
	}()
	Button(ButtonProps{Label: "x", Action: map[string]string{"data-fui-signal": "count"}}, nil)
}
