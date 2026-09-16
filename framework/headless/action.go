package headless

import (
	"strconv"
	"strings"

	"github.com/DonaldMurillo/gofastr/core-ui/html"
	"github.com/DonaldMurillo/gofastr/core/render"
)

// The optimistic mutation buttons, carrying GoFastr's
// optimistic-action contract so the framework's own runtime drives
// them: click paints the committed label immediately, the endpoint is
// fired, and a non-2xx rolls the label back. The HTTP response is the
// only authority — never a pushed event.
//
// Two components, one lifecycle. OptimisticAction commits once and
// stays committed ("Follow"); ToggleAction flips a binary the server
// owns ("Watch"/"Watching"), can ship already committed, and knows
// about mutex groups and untoggle.
//
// What the framework's runtime does NOT do is tell anyone: on failure
// it plays a shake and announces nothing. Both components here render
// a status span — role="status", polite, visually hidden — and put
// the failure sentence on the root as data-hui-action-failed, which is
// everything a runtime module that binds the hooks needs to turn the
// framework's bubbled rolled-back event into words.

// Action parts. The idle and done labels are parts of their own
// because a skin may want to weigh one differently from the other —
// a committed toggle that reads heavier than its idle sibling. The
// status span is PartVisuallyHidden because it must be read and must
// not be seen, the same rule as every other off-screen sentence in
// this package.
const (
	PartActionIdle Part = "action-idle"
	PartActionDone Part = "action-done"
)

// The failed-mutation sentence lives in Strings.ActionFailed; a
// sentence and not a bare "Error" is the point — see that field.

// checkActionEndpoint refuses the two endpoints that look wired and
// are not: none at all, and one the runtime will decline to fetch.
// The framework's origin check drops a cross-origin URL silently —
// the click does nothing — so the same-origin rule is enforced here,
// where the mistake is a panic at render time rather than a dead
// button in production.
func checkActionEndpoint(component, what, endpoint string) {
	if endpoint == "" {
		panic("headless: " + component + " requires " + what)
	}
	checkSameOrigin(component, what, endpoint)
}

// actionMethod canonicalises the method and returns the value to
// render: nothing for POST, which is the default the runtime already
// assumes, so an unstated method and an omitted attribute are the
// same request. Anything but the four verbs the runtime will send is
// refused rather than rendered into a fetch that cannot happen.
func actionMethod(component, method string) string {
	switch method {
	case "", "POST":
		return ""
	case "PUT", "PATCH", "DELETE":
		return method
	default:
		panic("headless: " + component + " Method must be POST, PUT, PATCH or DELETE, not " + method)
	}
}

// checkLabelsDiffer refuses two labels that read the same. The flip
// between them is the state change; with identical labels the only
// remaining signal is colour, which is the one signal this system
// never lets stand alone (WCAG 1.4.1). Whitespace and case are not a
// difference anyone hears.
func checkLabelsDiffer(component, idle, done string) {
	if strings.EqualFold(strings.TrimSpace(idle), strings.TrimSpace(done)) {
		panic("headless: " + component + " labels must differ — the flip from one to the other is the state change, and " +
			"two labels reading " + strconv.Quote(idle) + " leave colour as the only signal")
	}
}

// safeActionExtras is Safe with the prefixes the lifecycle owns added:
// a caller may not forge a data-hui-* hook any more than a data-fui-*
// one, and the names Safe takes exactly — type, disabled, data-state,
// aria-busy, aria-pressed — are the ones the runtime rewrites as the
// mutation moves. An extra that won any of them would desynchronise
// the button from its own lifecycle.
func safeActionExtras(extra html.Attrs) html.Attrs {
	return Safe(extra, "type", "disabled", "data-state", "aria-busy", "aria-pressed")
}

// OptimisticActionProps is a button that commits once: the success
// label is painted on click, the endpoint fired, and a non-2xx rolls
// everything back with a shake.
type OptimisticActionProps struct {
	// Endpoint is the URL the click fires against. Required, and
	// same-origin (it must start with "/").
	Endpoint string
	// Method defaults to POST and may be PUT, PATCH or DELETE.
	Method string
	// IdleLabel is the text at rest. Required.
	IdleLabel string
	// SuccessLabel is the text painted the moment the button is
	// clicked. Required — and it must say something the idle label
	// does not, because the label flip IS the state change; colour
	// alone is never allowed to carry it.
	SuccessLabel string
	// IdleIcon and DoneIcon are decoration beside each label. Each
	// span carries its own so the flip swaps glyph and word together.
	IdleIcon render.HTML
	DoneIcon render.HTML
	// Variant and Size are skin vocabulary, passed through so the
	// skin can look up "<part>--<variant>". The structure does not
	// care.
	Variant string
	Size    string
	// Disabled is the state at render time. The runtime manages it
	// from the first click: pending disables, settlement re-enables.
	Disabled bool
	// FailedText is what the status span announces when the server
	// refuses. Defaults to "Could not save. Try again."
	FailedText string

	ID         string
	ExtraAttrs html.Attrs

	// Parts: attrs only. Nothing is fillable — the two labels are
	// the state, and a slot that replaced one could make the button
	// say it did something it did not.
	Parts Parts
	// Strings are the strings this component says. Nil means the English
	// defaults; a layer above sets them from the request's language.
	Strings *Strings
}

// ToggleActionProps is the three-state cousin: idle → pending →
// committed, and back, with the initial state shipped from the server.
type ToggleActionProps struct {
	// Endpoint is the URL hit when toggling idle → committed.
	// Required, and same-origin.
	Endpoint string
	// Method defaults to POST and may be PUT, PATCH or DELETE. It
	// applies to both the commit and the untoggle request.
	Method string
	// IdleLabel is the text in the un-committed state. Required.
	IdleLabel string
	// CommittedLabel is the text while committed. Required, and it
	// must differ from IdleLabel: the flip is the signal.
	CommittedLabel string
	// IdleIcon and DoneIcon are decoration beside each label.
	IdleIcon render.HTML
	DoneIcon render.HTML
	// Committed is the state the server knows: render it true when
	// the action is already active, so first paint matches server
	// state instead of flashing through the wrong label.
	Committed bool
	// Group, when set, joins this button to a client-side mutex:
	// committing any button with the same key reverts the
	// previously-committed sibling, with no second request.
	Group string
	// AllowUntoggle lets a click on a committed button revert it to
	// idle. Without it (and without UntoggleEndpoint) the button is
	// sticky once committed, like OptimisticAction.
	AllowUntoggle bool
	// UntoggleEndpoint is the URL hit when reverting committed →
	// idle. Setting it implies AllowUntoggle; when empty the revert
	// flips locally with no request.
	UntoggleEndpoint string
	// Variant and Size are skin vocabulary.
	Variant string
	Size    string
	// Disabled is the state at render time.
	Disabled bool
	// FailedText is what the status span announces on a failure.
	FailedText string

	ID         string
	ExtraAttrs html.Attrs

	// Parts: attrs only, for the same reason as
	// OptimisticActionProps.
	Parts Parts
	// Strings are the strings this component says. Nil means the English
	// defaults; a layer above sets them from the request's language.
	Strings *Strings
}

// action is the half of the contract the two buttons share, so the
// shared half is rendered once. The differences are attributes and
// initial state, never structure: the runtime expects one shape from
// both, and a component that drifted would work until the page it was
// on was read by the other one's test.
type action struct {
	comp string // data-fui-comp: which runtime module drives it
	// wire is the attribute prefix that module reads its endpoint and
	// method from: "data-fui-optimistic" or "data-fui-toggle".
	wire     string
	idleAttr string // presence hook the runtime's setState flips hidden between
	doneAttr string

	endpoint string
	method   string // "" means POST and renders no attribute

	idleLabel string
	doneLabel string
	idleIcon  render.HTML
	doneIcon  render.HTML

	// state is "idle" or "committed". Pending is never shipped: it is
	// painted after a click and cleared when the response lands, and a
	// server that rendered it would send a button disabled and busy
	// before anyone had touched it.
	state string
	// pressed is the aria-pressed value, "" when the button is not a
	// toggle: OptimisticAction commits once and is not a pressed
	// control, so it claims no pressed state.
	pressed string

	group         string
	allowUntoggle bool
	untoggle      string

	variant    string
	size       string
	disabled   bool
	failedText string
	id         string
	extra      html.Attrs
	parts      Parts
	strings    *Strings
}

// OptimisticAction renders the button. The framework's runtime binds
// on [data-fui-comp="ui-optimistic-action"]; everything it needs —
// endpoint, method, both labels — is in the markup below.
func OptimisticAction(p OptimisticActionProps, s Skin) render.HTML {
	checkActionEndpoint("OptimisticAction", "Endpoint", p.Endpoint)
	if p.IdleLabel == "" {
		panic("headless: OptimisticAction requires IdleLabel")
	}
	if p.SuccessLabel == "" {
		panic("headless: OptimisticAction requires SuccessLabel")
	}
	checkLabelsDiffer("OptimisticAction", p.IdleLabel, p.SuccessLabel)
	return renderAction(action{
		comp:       "ui-optimistic-action",
		wire:       "data-fui-optimistic",
		idleAttr:   "data-fui-optimistic-idle",
		doneAttr:   "data-fui-optimistic-success",
		endpoint:   p.Endpoint,
		method:     actionMethod("OptimisticAction", p.Method),
		idleLabel:  p.IdleLabel,
		doneLabel:  p.SuccessLabel,
		idleIcon:   p.IdleIcon,
		doneIcon:   p.DoneIcon,
		state:      "idle",
		variant:    p.Variant,
		size:       p.Size,
		disabled:   p.Disabled,
		failedText: p.FailedText,
		id:         p.ID,
		extra:      p.ExtraAttrs,
		parts:      p.Parts,
		strings:    p.Strings,
	}, s)
}

// ToggleAction renders the button. The framework's runtime binds on
// [data-fui-comp="ui-toggle-action"], ships nothing itself but the
// initial state below, and mirrors committed onto aria-pressed from
// then on.
func ToggleAction(p ToggleActionProps, s Skin) render.HTML {
	checkActionEndpoint("ToggleAction", "Endpoint", p.Endpoint)
	if p.IdleLabel == "" {
		panic("headless: ToggleAction requires IdleLabel")
	}
	if p.CommittedLabel == "" {
		panic("headless: ToggleAction requires CommittedLabel")
	}
	checkLabelsDiffer("ToggleAction", p.IdleLabel, p.CommittedLabel)
	if p.UntoggleEndpoint != "" {
		checkActionEndpoint("ToggleAction", "UntoggleEndpoint", p.UntoggleEndpoint)
	}
	state, pressed := "idle", "false"
	if p.Committed {
		state, pressed = "committed", "true"
	}
	return renderAction(action{
		comp:      "ui-toggle-action",
		wire:      "data-fui-toggle",
		idleAttr:  "data-fui-toggle-idle",
		doneAttr:  "data-fui-toggle-committed",
		endpoint:  p.Endpoint,
		method:    actionMethod("ToggleAction", p.Method),
		idleLabel: p.IdleLabel,
		doneLabel: p.CommittedLabel,
		idleIcon:  p.IdleIcon,
		doneIcon:  p.DoneIcon,
		state:     state,
		pressed:   pressed,
		group:     p.Group,
		// Setting an untoggle endpoint is itself the request to be
		// untoggleable, the same implication the framework makes.
		allowUntoggle: p.AllowUntoggle || p.UntoggleEndpoint != "",
		untoggle:      p.UntoggleEndpoint,
		variant:       p.Variant,
		size:          p.Size,
		disabled:      p.Disabled,
		failedText:    p.FailedText,
		id:            p.ID,
		extra:         p.ExtraAttrs,
		parts:         p.Parts,
		strings:       p.Strings,
	}, s)
}

// renderAction draws the button both components are: the lifecycle
// attributes the framework's runtime reads, the announcement hooks
// ours writes to, and two label spans of which exactly the one
// matching the shipped state is visible.
func renderAction(a action, s Skin) render.HTML {
	own := Merge(safeActionExtras(a.extra), Attrs(map[string]string{
		"id":            a.id,
		"type":          "button",
		"data-fui-comp": a.comp,
	}))
	own[a.wire+"-endpoint"] = a.endpoint
	if a.method != "" {
		own[a.wire+"-method"] = a.method
	}
	if a.group != "" {
		own["data-fui-toggle-group"] = a.group
	}
	if a.allowUntoggle {
		own["data-fui-toggle-allow-untoggle"] = "true"
	}
	if a.untoggle != "" {
		own["data-fui-toggle-untoggle-endpoint"] = a.untoggle
	}
	own["data-state"] = a.state
	if a.pressed != "" {
		own["aria-pressed"] = a.pressed
	}
	// The sentence, on the root: the runtime's rolled-back listener
	// finds the button, and the span it writes to is inside it, so
	// the announcement never has to reach for anything the flip
	// already moved.
	own["data-hui-action-failed"] = orDefault(a.failedText, a.strings.Resolve().ActionFailed)
	Mark(own, "data-hui-action")
	Flag(own, "disabled", a.disabled)
	if cls := s.Variant(PartRoot, a.variant); cls != "" {
		own["class"] = cls
	}
	if cls := s.Variant(PartRoot, a.size); cls != "" {
		own["class"] = joinClasses(own["class"], cls)
	}

	b := a.parts.Box(s)
	kids := []render.HTML{
		actionSpan(b, PartActionIdle, a.idleAttr, a.state == "committed", a.idleLabel, a.idleIcon),
		actionSpan(b, PartActionDone, a.doneAttr, a.state != "committed", a.doneLabel, a.doneIcon),
		// The accessibility the framework's runtime does not ship.
		// A polite status region — role=status already means polite,
		// and stating it twice can announce twice: the flip is
		// visible, and this is how the rollback reaches anyone who
		// cannot see it.
		b.El("span", PartVisuallyHidden, Mark(Attrs(map[string]string{
			"role": "status",
		}), "data-hui-action-status")),
	}
	return b.El("button", PartRoot, own, kids...)
}

// actionSpan renders one of the two labels. hidden decides which of
// the pair carries it: the runtime flips that attribute and nothing
// else, so the server ships exactly the opposite pair and the first
// paint already agrees with the state attribute above it.
func actionSpan(b Box, part Part, hook string, hidden bool, label string, icon render.HTML) render.HTML {
	attrs := html.Attrs{hook: ""}
	if hidden {
		Mark(attrs, "hidden")
	}
	kids := make([]render.HTML, 0, 2)
	if icon != "" {
		kids = append(kids, b.El("span", PartIcon,
			Attrs(map[string]string{"aria-hidden": "true"}), icon))
	}
	kids = append(kids, render.Text(label))
	return b.El("span", part, attrs, kids...)
}

func init() {
	Register(Spec{
		Name:    "OptimisticAction",
		Anatomy: []Part{PartRoot, PartIcon, PartActionIdle, PartActionDone, PartVisuallyHidden},
		Hooks:   []string{"data-hui-action", "data-hui-action-failed", "data-hui-action-status"},
		WithParts: func(s Skin, parts Parts) render.HTML {
			return OptimisticAction(OptimisticActionProps{
				Endpoint: "/follow", IdleLabel: "Follow", SuccessLabel: "Following",
				Parts: parts,
			}, s)
		},
		Cases: func(k Kit) []Case {
			s := k.Skin
			return []Case{{
				Name: "default",
				Why: "both labels ship in the SSR so the optimistic flip needs no round-trip, the method is " +
					"absent because POST is the runtime's default, and the status span exists because the " +
					"framework's shake announces nothing",
				HTML: OptimisticAction(OptimisticActionProps{
					Endpoint: "/apps/blog/restart", IdleLabel: "Restart now", SuccessLabel: "Restarting",
					Variant: "primary",
				}, s),
			}, {
				Name: "with icons",
				Why: "each label carries its own icon so the flip swaps glyph and word together, and both are " +
					"aria-hidden because the words are the meaning — a check mark nobody can hear says nothing",
				HTML: OptimisticAction(OptimisticActionProps{
					Endpoint: "/apps/blog/pin", IdleLabel: "Pin", SuccessLabel: "Pinned",
					IdleIcon: SpecimenGlyph, DoneIcon: SpecimenGlyph, Variant: "secondary",
				}, s),
			}, {
				Name: "delete, not post",
				Why: "a method other than POST is published as an attribute, because the runtime defaults to " +
					"POST and an unstated DELETE would fire the wrong request entirely",
				HTML: OptimisticAction(OptimisticActionProps{
					Endpoint: "/webhooks/9", Method: "DELETE",
					IdleLabel: "Remove webhook", SuccessLabel: "Removed", Variant: "danger",
				}, s),
			}, {
				Name: "disabled",
				Why: "a disabled optimistic button still ships both labels and its announcement hook — " +
					"disabling is a state of the click, not of the contract, and the runtime re-enables " +
					"the button itself while a mutation is in flight",
				HTML: OptimisticAction(OptimisticActionProps{
					Endpoint: "/apps/blog/redeploy", IdleLabel: "Redeploy", SuccessLabel: "Redeploying",
					Variant: "ghost", Disabled: true,
				}, s),
			}}
		},
	})

	Register(Spec{
		Name:    "ToggleAction",
		Anatomy: []Part{PartRoot, PartIcon, PartActionIdle, PartActionDone, PartVisuallyHidden},
		Hooks:   []string{"data-hui-action", "data-hui-action-failed", "data-hui-action-status"},
		WithParts: func(s Skin, parts Parts) render.HTML {
			return ToggleAction(ToggleActionProps{
				Endpoint: "/watch", IdleLabel: "Watch", CommittedLabel: "Watching",
				Parts: parts,
			}, s)
		},
		Cases: func(k Kit) []Case {
			s := k.Skin
			return []Case{{
				Name: "idle",
				Why: "aria-pressed false and the committed span hidden: first paint matches the server's " +
					"answer, and the runtime only ever flips what is already there",
				HTML: ToggleAction(ToggleActionProps{
					Endpoint: "/apps/blog/watch", IdleLabel: "Watch", CommittedLabel: "Watching",
					Variant: "primary",
				}, s),
			}, {
				Name: "committed at load",
				Why: "the server knows: aria-pressed is true and it is the idle span that is hidden, so a page " +
					"that loads already-watching does not flash through the wrong label first — and each label " +
					"carries its own aria-hidden icon, so the flip swaps glyph and word together",
				HTML: ToggleAction(ToggleActionProps{
					Endpoint: "/apps/blog/watch", IdleLabel: "Watch", CommittedLabel: "Watching",
					IdleIcon: SpecimenGlyph, DoneIcon: SpecimenGlyph,
					Committed: true, Variant: "secondary",
				}, s),
			}, {
				Name: "two in a mutex group",
				Why: "committing one member of a group revokes the other with no second request, so a plan " +
					"picker reads as one choice rather than three independent buttons that can all be on",
				HTML: group(
					ToggleAction(ToggleActionProps{
						Endpoint: "/plan/starter", IdleLabel: "Starter", CommittedLabel: "Starter ✓",
						Group: "plan", Committed: true, Variant: "ghost",
					}, s),
					ToggleAction(ToggleActionProps{
						Endpoint: "/plan/pro", IdleLabel: "Pro", CommittedLabel: "Pro ✓",
						Group: "plan", Variant: "ghost",
					}, s)),
			}, {
				Name: "untoggle, with its own endpoint",
				Why: "a committed button that can be clicked again publishes the untoggle endpoint and ships " +
					"committed — the only state from which untoggle is reachable — so the revert is a request " +
					"the server can refuse, not just a local flip",
				HTML: ToggleAction(ToggleActionProps{
					Endpoint: "/plan/pro", UntoggleEndpoint: "/plan/none",
					IdleLabel: "Pro", CommittedLabel: "Pro ✓", Committed: true,
					AllowUntoggle: true, Variant: "primary",
				}, s),
			}}
		},
	})
}
