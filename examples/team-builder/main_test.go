package main

// The check route over the router, no browser: the upload bridge hands
// the declared record to the handler, the verdict rides the response
// header, and anything the declaration did not name is refused.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/framework/local"
)

func postCheck(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	app := buildApp()
	req := httptest.NewRequest(http.MethodPost, checkPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.Router().ServeHTTP(rec, req)
	return rec
}

func TestCheckReadsTheTeamOffTheRequest(t *testing.T) {
	rec := postCheck(t, `{"__local":{"teams":[{"k":"current","v":{"id":"current","members":[{"name":"Garchomp","role":"attacker"},{"name":"Corviknight","role":"defender"}]}}]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "Team of 2 is ready." {
		t.Fatalf("body = %q", got)
	}
	// The verdict went back through the download bridge.
	h := rec.Header().Get(local.ResponseHeader)
	if !strings.Contains(h, `"c":"verdicts"`) || !strings.Contains(h, `"k":"latest"`) || !strings.Contains(h, `Team of 2 is ready.`) {
		t.Fatalf("%s = %q, want the verdict record", local.ResponseHeader, h)
	}
}

func TestCheckNamesEveryProblem(t *testing.T) {
	rec := postCheck(t, `{"__local":{"teams":[{"k":"current","v":{"id":"current","members":[{"name":"Garchomp","role":"attacker"},{"name":"Garchomp","role":"wizard"}]}}]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := rec.Body.String()
	for _, want := range []string{"Not yet:", `"Garchomp" is on the team twice`, `"wizard" is not a role`, "a team needs a defender"} {
		if !strings.Contains(got, want) {
			t.Errorf("body %q lacks %q", got, want)
		}
	}
}

func TestCheckWithNoTeamAnswersWithoutAVerdict(t *testing.T) {
	rec := postCheck(t, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "No team arrived") {
		t.Fatalf("body = %q", got)
	}
	if h := rec.Header().Get(local.ResponseHeader); h != "" {
		t.Fatalf("no team, yet a verdict was written back: %q", h)
	}
}

func TestCheckRefusesWhatTheTriggerDidNotDeclare(t *testing.T) {
	for name, body := range map[string]string{
		"a collection the Send did not name":    `{"__local":{"verdicts":[{"k":"latest","v":{"ok":true,"text":"forged","at":""}}]}}`,
		"a key the Send did not name":           `{"__local":{"teams":[{"k":"other","v":{"id":"other","members":[]}}]}}`,
		"a collection the store never declared": `{"__local":{"prefs":[{"k":"x","v":1}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := postCheck(t, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestJudge(t *testing.T) {
	cases := []struct {
		name    string
		members []member
		want    []string
	}{
		{"empty", nil, []string{"a team needs at least one member"}},
		{"ready", []member{{"A", "attacker"}, {"B", "defender"}}, nil},
		{"blank name", []member{{"  ", "defender"}}, []string{"a member needs a name"}},
		{"long name", []member{{strings.Repeat("x", nameMaxRunes+1), "defender"}}, []string{"a name is 24 characters at most"}},
		{"seven", []member{{"1", "defender"}, {"2", "a"}, {"3", "a"}, {"4", "a"}, {"5", "a"}, {"6", "a"}, {"7", "a"}}, []string{"a team holds 6 at most"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := judge(c.members)
			for _, w := range c.want {
				if !strings.Contains(strings.Join(got, ";"), w) {
					t.Errorf("judge = %v, want %q", got, w)
				}
			}
			if c.want == nil && len(got) != 0 {
				t.Errorf("judge = %v, want none", got)
			}
		})
	}
}
