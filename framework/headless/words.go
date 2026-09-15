package headless

// Words: every string a component says, behind one seam.
//
// The headless layer is the accessibility half of the system, and a
// surprising amount of what it guarantees is wording: the tone said
// before an alert's title, the name of the button that reveals a
// password, "Previous" on a pager's disabled end. A
// French reader needs those guarantees in French, and a key map
// ("dismiss.label" → "Fermer") would put the finding of a missing
// string on a rendered page. So the words are a typed struct: one
// field per string, a doc comment saying its shape, and a missing
// field a compile error rather than a stray English word on a French
// page. The framework's own translated strings live in
// framework/i18nui as keys; a layer above this one resolves each
// field from those keys once per request.
//
// The seam is Seams, which already reaches every component: nil Words
// means the English defaults below, which is what the goldens pin.
// Nothing here fetches, caches or guesses a language.
//
// Plurals are two fields, One and Many, chosen by the component and
// never built by appending "s". Formats keep their verbs: a
// translation may reorder the words but must keep the placeholders,
// which the gates in words_test.go hold.

import (
	"reflect"
	"strings"
)

// Words are the strings the components say. Every field defaults to
// the English the components rendered before this type existed, so a
// nil or partially-set value is safe; DefaultWords returns them all
// and ProbeWords returns one probe token per field.
//
// Fields holding a %s or %d are format strings, applied with
// fmt.Sprintf at the site that owns the numbers. {n} is substituted
// by the runtime, not the server: those strings travel as data-*
// attributes and the count is written in when it is known.
type Words struct {
	// ─── said by more than one component ───────────────────────────
	//
	// The same English word doing the same job in several components
	// is one field, so one translation serves every speaker of it.

	// DismissTitled names a dismiss control whose target follows it:
	// an Alert's, a SystemBanner's. The title follows the colon and a
	// space; a format taking it as %s.
	DismissTitled string
	// RemoveLabelled names a remove control by what it removes: a
	// Tag's chip. A format taking the value's name as %s.
	RemoveLabelled string

	// ─── OptimisticAction / ToggleAction ───────────────────────────

	// ActionFailed is what a failed mutation announces when the caller
	// supplied nothing better. A sentence, not a word: it says the
	// save did not happen and that trying again is allowed.
	ActionFailed string

	// ─── Color ──────────────────────────────────────────────────────

	// PickColor names the colour swatch, which carries no visible
	// label. A format taking the field's name as %s.
	PickColor string

	// ─── Password ───────────────────────────────────────────────────

	// ShowPassword and HidePassword name the reveal button in each of
	// its two states. Imperative, not rendered visually; they also
	// travel as data so the runtime can swap them.
	ShowPassword string
	HidePassword string
	// RevealShow is the reveal button's visible text, and RevealHide
	// is what the runtime swaps it to. One word each; RevealHide is
	// never rendered by the server.
	RevealShow string
	RevealHide string

	// ─── Pagination ─────────────────────────────────────────────────

	// Previous and Next label the pager's ends, which are disabled
	// anchors rather than absent ones. One word each; they carry no
	// arrows, so a translation may add its own.
	Previous string
	Next     string

	// ─── SystemBanner, Alert ────────────────────────────────────────

	// ToneInfo, ToneSuccess, ToneWarning and ToneDanger are the tone
	// said before a title — a SystemBanner's, and an Alert's when the
	// caller passes one as ToneWord — because the title says WHAT
	// happened and the
	// tone is the only thing saying how serious it is. One word each,
	// the one a reader understands rather than the stylesheet's name
	// for the colour ("Error", not "Danger"); the colon and space that
	// follow are added in the assembly.
	ToneInfo    string
	ToneSuccess string
	ToneWarning string
	ToneDanger  string

	// ─── ValidationSummary ──────────────────────────────────────────

	// ThereIsAProblem heads the list a failed submit focuses. A short
	// sentence; it names the fact, the list names each field.
	ThereIsAProblem string
}

// defaultWords is the English the components rendered before Words
// existed. The goldens pin these bytes.
var defaultWords = Words{
	DismissTitled:  "Dismiss: %s",
	RemoveLabelled: "Remove %s",

	ActionFailed: "Could not save. Try again.",

	PickColor: "Pick %s",

	ShowPassword: "Show password", // not-a-secret: the reveal button's accessible name
	HidePassword: "Hide password", // not-a-secret: the reveal button's accessible name
	RevealShow:   "Show",
	RevealHide:   "Hide",

	Previous: "Previous",
	Next:     "Next",

	ToneInfo:    "Information",
	ToneSuccess: "Success",
	ToneWarning: "Warning",
	ToneDanger:  "Error",

	ThereIsAProblem: "There is a problem",
}

// DefaultWords returns a fresh copy of the English defaults, every
// field set. Fresh so a caller cannot mutate the package's copy
// through it.
func DefaultWords() *Words {
	w := defaultWords
	return &w
}

// withDefaults returns w with every empty field filled from the
// English defaults; w itself is left alone.
func (w *Words) withDefaults() *Words {
	out := DefaultWords()
	src := reflect.ValueOf(w).Elem()
	dst := reflect.ValueOf(out).Elem()
	for i := 0; i < src.NumField(); i++ {
		v := src.Field(i).String()
		if v == "" {
			continue
		}
		f := dst.Field(i)
		if !f.CanSet() {
			panic("headless: Words." + src.Type().Field(i).Name + " is not settable — every field of Words must be an exported string")
		}
		f.SetString(v)
	}
	return out
}

// ProbeWords returns a Words whose every field is its own name in
// angle brackets — formats as the name plus their placeholders, so
// `<RemoveLabelled env=prod>` renders where "Remove env=prod" would. A render against it shows exactly which words came
// through the seam; a real English word in one is a word that
// bypassed it.
func ProbeWords() *Words {
	w := DefaultWords()
	v := reflect.ValueOf(w).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		def := v.Field(i).String()
		probe := "<" + t.Field(i).Name
		if ph := placeholdersIn(def); len(ph) > 0 {
			probe += " " + strings.Join(ph, " ")
		}
		f := v.Field(i)
		if !f.CanSet() {
			panic("headless: Words." + t.Field(i).Name + " is not settable — every field of Words must be an exported string")
		}
		f.SetString(probe + ">")
	}
	return w
}

// placeholdersIn lists a default's placeholders in order: % verbs and
// {name} tokens, skipping the escaped %%. ProbeWords keeps them so a
// probe render still applies its arguments, and the validation test
// compares them so a translation cannot drop one.
func placeholdersIn(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%':
			if i+1 < len(s) {
				if s[i+1] == '%' {
					i++
					continue
				}
				out = append(out, s[i:i+2])
				i++
			}
		case '{':
			if end := strings.IndexByte(s[i:], '}'); end > 0 {
				out = append(out, s[i:i+end+1])
				i += end
			}
		}
	}
	return out
}

// wordsProbe, when set, is what every component says instead of the
// English defaults. Only the harness sets it, to render the whole
// corpus through the words seam without editing every fixture; it is
// nil in production and W() behaves as if it did not exist.
var wordsProbe *Words

// W is how a component reaches its words: the caller's when a layer
// above resolved them from the request, the harness's probe when a
// test is looking, and the English defaults otherwise. A caller's
// Words may be partial: every empty field falls back to its English
// default, so a Words that sets one string does not silently unname
// the reveal button.
func (s Seams) W() *Words {
	if s.Words != nil {
		return s.Words.withDefaults()
	}
	if wordsProbe != nil {
		return wordsProbe
	}
	return DefaultWords()
}
