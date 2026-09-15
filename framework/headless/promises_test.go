package headless

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core/render"
)

// A badge's tone is information: "warning" beside a restart count is
// why the count matters. When the tone lives only in a class it
// reaches nobody who cannot see the colour (WCAG 1.4.1) — Alert and
// SystemBanner say their tone in words, and a badge owes the same.
//
// A badge's label is its whole meaning. The tone the skin paints is
// decoration for the word already there, so no hidden tone word is
// injected: "Warning: restarting" would read the colour to someone who
// already heard the status, and a badge whose colour means something
// its label does not say has the wrong label.
func TestBadgeMeansWhatItsLabelSays(t *testing.T) {
	got := Badge(BadgeProps{Label: "restarting"},
		Skin{PartRoot: "ds-badge ds-badge--warning"})
	has(t, got, ">restarting<", "the label is not the badge's text")
	strip := regexp.MustCompile(`<[^>]*>`)
	if words := strings.TrimSpace(strip.ReplaceAllString(string(got), "")); words != "restarting" {
		t.Errorf("a badge reads more than its label: %q — the tone is decoration and must not be spoken", words)
	}
}

// The swatch and the hex text are one control with two faces, and a
// person using the picker believes the number they can read. If the
// two carry different values at render, the control lies about the
// colour it is editing — the runtime keeps them in step afterwards,
// but the server sent the first lie.
func TestColorSwatchAndTextCarryOneValue(t *testing.T) {
	got := Color(ColorProps{Name: "accent", Value: "#0EA5E9"}, nil)
	if n := count(got, `value="#0EA5E9"`); n != 2 {
		t.Errorf("the value reached %d of the two faces — the swatch and the text disagree at render", n)
	}
	if n := count(got, `name="accent"`); n != 1 {
		t.Errorf("the name landed on %d controls — exactly one face of the pair may submit", n)
	}
}

// A textarea's hint — "Markdown welcome", "one paragraph max" — is on
// screen either way; without the described-by wiring a screen reader
// stops at the label and the format rule never arrives. The field
// builds the relationship from the ids it hands the control, so the
// wiring cannot disagree with the hint it renders.
func TestTextareaIsNamedByItsFieldWiring(t *testing.T) {
	got := Field(FieldProps{Label: "Release notes", For: "notes", Hint: "Markdown welcome"}, nil,
		func(c FieldControl) render.HTML {
			return Textarea(TextareaProps{Name: "notes", ID: c.ID, DescribedBy: c.DescribedBy}, nil)
		})
	has(t, got, "<textarea", "the control is not a textarea")
	has(t, got, `for="notes"`, "the label does not point at the control")
	has(t, got, `aria-describedby="notes-hint"`, "the hint is on screen and absent to a screen reader")
}

// The file input is the control: it is what submits, what the
// keyboard operates and what the zone opens the picker through. A
// display:none would keep it in the source and take it out of
// everything else, so the structure keeps it in the tree and refuses
// to hide it itself — the skin may shrink it, never remove it. The
// zone is its <label>, bound by the for/id pair the ID requirement
// exists to keep whole.
func TestFileUploadKeepsTheInputInTheTree(t *testing.T) {
	got := FileUpload(FileUploadProps{Name: "backup", ID: "up",
		Label: "Drag a backup here", CTA: "choose a file", Hint: ".tar.gz up to 2 GB"}, nil)
	has(t, got, `type="file"`, "the real file input is not in the tree")
	has(t, got, `id="up"`, "the input has no id for the zone to bind to")
	has(t, got, `for="up"`, "the zone is not the input's label — the biggest click target on the control opens nothing")
	hasNot(t, got, "hidden", "the input is hidden from the tree rather than only from sight")
	hasNot(t, got, "aria-hidden", "the input is taken out of the accessibility tree")
}

// role="toolbar" promises arrow-key roving between the controls, and
// this script-free package cannot keep that promise; a toolbar that
// claims the role and tabs like a div misleads the one reader who
// relied on it. No role rather than a hollow one. A caller who ships the keyboard handling
// may add it through ExtraAttrs, and that path is kept open.
func TestToolbarDoesNotClaimTheToolbarPattern(t *testing.T) {
	got := Toolbar(ToolbarProps{}, nil,
		Button(ButtonProps{Label: "New app"}, nil))
	hasNot(t, got, `role="toolbar"`, "the toolbar claims a role whose keyboard contract nothing here implements")
	with := Toolbar(ToolbarProps{ExtraAttrs: Attrs(map[string]string{"role": "toolbar", "aria-label": "Apps"})}, nil,
		Button(ButtonProps{Label: "New app"}, nil))
	has(t, with, `role="toolbar"`, "a caller who ships the roving keyboard cannot add the role")
}
