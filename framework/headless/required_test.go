package headless

import (
	"strings"
	"testing"
)

// refuse asserts that fn panics and that the panic names the missing
// prop, so the reader of the stack trace is told what to fix rather
// than only where it broke.
func refuse(t *testing.T, prop string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("rendering without %s did not panic", prop)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic is a %T, not the string the components speak: %v", r, r)
		}
		if !strings.Contains(msg, prop) {
			t.Errorf("the panic does not name %s: %s", prop, msg)
		}
		if !strings.HasPrefix(msg, "headless: ") {
			t.Errorf("the panic is not in this package's voice: %s", msg)
		}
	}()
	fn()
}

// A control with no name submits nothing: the browser drops it from
// the form data entirely, so the value a person typed arrives nowhere.
func TestInputRefusesANamelessControl(t *testing.T) {
	refuse(t, "Name", func() { Input(InputProps{}, nil) })
}

func TestTextareaRefusesANamelessControl(t *testing.T) {
	refuse(t, "Name", func() { Textarea(TextareaProps{}, nil) })
}

func TestSelectRefusesANamelessControl(t *testing.T) {
	refuse(t, "Name", func() { Select(SelectProps{}, nil) })
}

func TestPasswordRefusesANamelessControl(t *testing.T) {
	refuse(t, "Name", func() { Password(PasswordProps{}, nil) })
}

func TestColorRefusesANamelessControl(t *testing.T) {
	refuse(t, "Name", func() { Color(ColorProps{}, nil) })
}

// A choice with no label is a small box nobody can name: the label
// wrapping the control IS its accessible name and its hit area.
func TestChoiceRefusesAnUnlabelledControl(t *testing.T) {
	refuse(t, "Label", func() { Choice(ChoiceProps{Type: "checkbox"}, nil) })
}

func TestSwitchRefusesAnUnlabelledControl(t *testing.T) {
	refuse(t, "Label", func() { Switch(SwitchProps{}, nil) })
}

// A form with no action posts to the current URL: the framework's
// silent default, and a failed submit then quietly re-renders the
// same page with no error anyone can see.
func TestFormRefusesAnActionlessSubmit(t *testing.T) {
	refuse(t, "Action", func() { Form(FormProps{}, nil) })
}

// The navigation components refuse in the package's own voice, so a
// caller reading a panic knows which layer spoke. Pinned because these
// refusals were the ones a port left in another package's name.
func TestPaginationRefusesAnUnnamedNav(t *testing.T) {
	refuse(t, "AriaLabel", func() {
		Pagination(PaginationProps{Page: 1, Pages: 2, HrefPattern: "/x?p=%d", Island: fixtureIsland}, nil)
	})
}
