// Package main is the smallest framework/local example: a team builder
// for a battle simulator. The team is the user's, lives in their
// browser, and needs no account. Quit the browser, come back tomorrow,
// the team is still there. One binary, no database.
//
// The three beats it exists to show:
//
//   - The team is kept by the browser, not the server. The page script
//     writes the roster through the store the Go declaration
//     generated (__gofastr.localStore('team-builder')), into the
//     kernel's IndexedDB primitive. The server never sees it and
//     never remembers it.
//   - The server reads it only when asked. "Check team" is an RPC
//     island whose trigger declares that teams:current rides the
//     request; the Go handler reads the record with local.Get,
//     judges it, and answers. Nothing else in the store ever leaves
//     the browser.
//   - The server can write back. The verdict goes into the response
//     header the download bridge reads, so the browser keeps it beside
//     the team, and it too survives quitting the browser.
//
// Before framework/local every app that wanted this wrote its own
// island with a document script over raw IndexedDB or localStorage:
// an invented key namespace, no size caps, no schema version, and a
// hand-rolled way to get the value into a request. Here the
// declaration is eleven lines of Go and the page script only paints.
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	uiapp "github.com/DonaldMurillo/gofastr/core-ui/app"
	"github.com/DonaldMurillo/gofastr/core-ui/component"
	"github.com/DonaldMurillo/gofastr/core-ui/html"
	"github.com/DonaldMurillo/gofastr/core-ui/store"
	"github.com/DonaldMurillo/gofastr/core/render"
	"github.com/DonaldMurillo/gofastr/framework"
	"github.com/DonaldMurillo/gofastr/framework/isolation"
	"github.com/DonaldMurillo/gofastr/framework/local"
	"github.com/DonaldMurillo/gofastr/framework/ui"
	"github.com/DonaldMurillo/gofastr/framework/ui/theme"
	"github.com/DonaldMurillo/gofastr/framework/uihost"
)

//go:embed static/app.js
var appJS []byte

// defaultAddr is the fallback when $PORT is unset; `gofastr dev` and
// PaaS runtimes inject PORT and isolation.ListenAddr honours it.
const defaultAddr = ":8094"

const (
	checkPath     = "/check"
	appScriptPath = "/__team/app.js"
	// teamSize is the most members a team holds.
	teamSize = 6
	// nameMaxRunes bounds a member's name.
	nameMaxRunes = 24
)

// roles a member can take. The select renders them; the server
// refuses anything else, because the browser is the user's and the
// record is input like any other.
var roles = []string{"attacker", "defender", "support"}

// member is one slot of a team.
type member struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// team is the record the browser keeps. The key field is id, so the
// page script's put(value) reads it from the record.
type team struct {
	ID      string   `json:"id"`
	Members []member `json:"members"`
}

// verdict is what the server writes back after a check.
type verdict struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
	At   string `json:"at"`
}

var (
	// One store per app, declared once. The app id is the namespace
	// every record lives under in the browser.
	teamLocal = local.New("team-builder")
	// The teams: a small record, a handful of them, keyed by id.
	teams = local.Define[team](teamLocal, "teams", local.CollectionConfig{
		Version: 1, KeyField: "id", MaxRecordBytes: 8 << 10, MaxRecords: 20,
	})
	// The verdict the server pushed back last. One record is enough.
	verdicts = local.Define[verdict](teamLocal, "verdicts", local.CollectionConfig{
		Version: 1, MaxRecords: 1,
	})

	// The check result, painted by the RPC response.
	teamStore   = store.New("team")
	checkResult = teamStore.String("check", "Not checked yet.")
	// The upload bridge: only teams:current rides the check request.
	checkSend = local.Send(teams.Key("current"))
)

func main() {
	fwApp := buildApp()
	addr, err := isolation.ListenAddr(".", defaultAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("team-builder listening on %s", addr)
	if err := fwApp.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// buildApp wires the site, the host, the store's manifest, the page
// script and the check route, without binding a port, so tests drive
// fwApp.Router() directly.
func buildApp() *framework.App {
	site := uiapp.NewApp("team-builder")
	site.WithTheme(theme.Default())
	site.SetDefaultLayout(uiapp.NewLayout("main").WithContainer().WithHeader(&siteHeaderComponent{}))
	site.RegisterScreen(uiapp.NewScreen("/", &TeamScreen{}).WithTitle("Team builder"), nil)

	// The store's manifest and the page script ride the extra-script
	// rail after runtime.js: the declaration reaches the browser as a
	// script, never as markup (CSP-clean, no inline JavaScript).
	host := uihost.New(site,
		uihost.WithExtraScripts(teamLocal.ScriptURL(), uihost.ScriptURL(appScriptPath, appJS)),
	)

	fwApp := framework.NewApp(
		framework.WithConfig(framework.AppConfig{Name: "team-builder"}),
	)
	fwApp.Mount(host)

	rt := fwApp.Router()
	rt.Get(teamLocal.ScriptPath(), teamLocal.ScriptHandler())
	rt.Get(appScriptPath, uihost.ScriptHandler(appJS))
	// HandlerFunc wraps checkTeam in the upload bridge: the declared
	// record is read off the body, anything undeclared is refused,
	// the caps are enforced, and the record is on the context.
	rt.Post(checkPath, checkSend.HandlerFunc(checkTeam))
	return fwApp
}

// siteHeaderComponent is the shared chrome.
type siteHeaderComponent struct{}

func (h *siteHeaderComponent) Render() render.HTML {
	return ui.SiteHeader(ui.SiteHeaderConfig{
		Brand: ui.Link(ui.LinkConfig{Href: "/", Text: "Team builder"}),
	})
}

// TeamScreen is the one page.
type TeamScreen struct{ component.ContextOnly }

func (s *TeamScreen) ScreenTitle() string { return "Team builder" }

func (s *TeamScreen) RenderCtx(ctx context.Context) render.HTML {
	options := make([]ui.SelectOption, 0, len(roles))
	for _, r := range roles {
		options = append(options, ui.SelectOption{Value: r, Text: r})
	}

	return ui.Stack(ui.StackConfig{Gap: ui.GapLG},
		ui.PageHeader(ui.PageHeaderConfig{
			Eyebrow:  "GoFastr example",
			Title:    "Team builder",
			Subtitle: "Your team lives in this browser. No account, nothing on the server. Quit the browser and come back: it is still here.",
		}),
		ui.Section(ui.SectionConfig{
			Heading:     "Your team, kept in this browser",
			Description: "Every change is written to teams:current through the store the Go declaration generated. The server never sees the roster until you ask it to.",
			Ctx:         ctx,
		},
			ui.Card(ui.CardConfig{},
				ui.Stack(ui.StackConfig{Gap: ui.GapMD},
					ui.TextField(ui.TextFieldConfig{
						Name: "name", Label: "Name", ID: "member-name",
						MaxLength: nameMaxRunes, Placeholder: "Garchomp",
					}),
					ui.Select(ui.SelectConfig{
						Name: "role", Label: "Role", ID: "member-role", Options: options,
					}),
					ui.Cluster(ui.ClusterConfig{Gap: ui.GapSM},
						ui.Button(ui.ButtonConfig{Label: "Add to team", ID: "add-member"}),
						ui.Button(ui.ButtonConfig{Label: "Clear team", ID: "clear-team", Variant: ui.ButtonSecondary}),
					),
				),
			),
			html.UnorderedList(html.ListConfig{ID: "roster"}),
			html.Paragraph(html.TextConfig{ID: "roster-count"}, render.Text("No members yet.")),
		),
		ui.Section(ui.SectionConfig{
			Heading:     "Check it on the server",
			Description: "The button is an RPC island. Its trigger declares that teams:current accompanies the request, the Go handler reads it with local.Get and judges it, and the verdict rides the response back into the browser, where it is kept beside the team.",
			Ctx:         ctx,
		},
			ui.Form(ui.FormConfig{
				Action:      checkPath,
				SubmitLabel: "Check team",
				ID:          "check-form",
				Ctx:         ctx,
				// The RPC attributes make the form an island; Merge adds
				// the upload declaration (data-local-send) and the module
				// the runtime loads before dispatching (data-fui-rpc-with).
				ExtraAttrs: checkSend.Merge(html.Attrs{
					"data-fui-rpc":        checkPath,
					"data-fui-rpc-signal": checkResult.Name(),
				}),
			},
				html.Paragraph(html.TextConfig{}, render.Text("A team is ready when it has one to six members, no two with the same name, and at least one defender.")),
			),
			checkResult.Bind(ctx, "p", map[string]string{"id": "check-result"}),
			html.Paragraph(html.TextConfig{ID: "last-verdict"}, render.Text("No verdict kept yet.")),
		),
	)
}

// checkTeam runs inside the upload bridge: teams:current, if the
// browser sent it, is already on the context.
func checkTeam(w http.ResponseWriter, r *http.Request) {
	// src says which channel the team arrived on. This handler only
	// accepts the upload: teams is not a mirrored collection, so a
	// cookie could never answer here, and asserting it keeps that true
	// if the declaration ever changes.
	t, src, err := local.Get(r.Context(), teams, "current")
	if err != nil {
		http.Error(w, "the team does not decode", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if src != local.SourceUpload {
		fmt.Fprint(w, "No team arrived: add a member first.")
		return
	}
	problems := judge(t.Members)
	text := fmt.Sprintf("Team of %d is ready.", len(t.Members))
	if len(problems) > 0 {
		text = "Not yet: " + strings.Join(problems, "; ") + "."
	}
	// The download bridge: the verdict rides the response header and
	// the browser keeps it beside the team.
	_ = local.Put(w, verdicts, "latest", verdict{
		OK: len(problems) == 0, Text: text, At: time.Now().UTC().Format(time.RFC3339),
	})
	fmt.Fprint(w, text)
}

// judge says what keeps a roster from being a team. The record came
// from the user's browser, so every field is checked like any input.
func judge(members []member) []string {
	var problems []string
	if len(members) == 0 {
		return []string{"a team needs at least one member"}
	}
	if len(members) > teamSize {
		problems = append(problems, fmt.Sprintf("a team holds %d at most", teamSize))
	}
	seen := make(map[string]bool, len(members))
	defenders := 0
	for _, m := range members {
		switch n := utf8.RuneCountInString(m.Name); {
		case strings.TrimSpace(m.Name) == "":
			problems = append(problems, "a member needs a name")
		case n > nameMaxRunes:
			problems = append(problems, fmt.Sprintf("a name is %d characters at most", nameMaxRunes))
		case seen[m.Name]:
			problems = append(problems, fmt.Sprintf("%q is on the team twice", m.Name))
		default:
			seen[m.Name] = true
		}
		if !validRole(m.Role) {
			problems = append(problems, fmt.Sprintf("%q is not a role", m.Role))
		}
		if m.Role == "defender" {
			defenders++
		}
	}
	if defenders == 0 {
		problems = append(problems, "a team needs a defender")
	}
	return problems
}

func validRole(role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}
