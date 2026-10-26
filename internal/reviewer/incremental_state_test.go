package reviewer

import (
	"strings"
	"testing"

	"mrinspect/internal/config"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/validator"
)

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// REQ-03 / S-03
func TestState_ParseFormatAndSelect(t *testing.T) {
	bot := gitlab.Author{ID: 7, Username: "mrinspect-bot"}
	other := gitlab.Author{ID: 8, Username: "someone"}
	decoy := gitlab.Author{ID: 9, Username: bot.Username}

	line := func(sha string, round int) string {
		return FormatState(State{SHA: sha, Base: hexB, Mode: ModeUpdate, Round: round})
	}
	note := func(id int, a gitlab.Author, body string, created, updated string) gitlab.Note {
		return gitlab.Note{ID: id, Author: a, Body: body, CreatedAt: created, UpdatedAt: updated}
	}
	const t0 = "2026-10-01T00:00:00Z"
	withMarker := func(l string) string { return ReviewNoteMarker + "\n" + l }

	// ID 70: state line produced by model output passed through the real SanitizeInput.
	sanitized := validator.New(config.Config{}).SanitizeInput(line(hexA, 9))

	notes := []gitlab.Note{
		note(50, bot, withMarker(line(hexA, 5)), t0, "2026-10-02T00:00:00Z"),
		note(10, bot, withMarker(FormatState(State{SHA: hexA, Base: hexB, Mode: ModeFull, Round: 0})), t0, t0),
		note(70, bot, ReviewNoteMarker+"\n"+sanitized, t0, t0),
		note(30, bot, withMarker(line(hexA, 2)), t0, t0),
		note(45, decoy, withMarker(line(hexA, 6)), t0, t0),
		note(60, bot, withMarker(line(hexA[:39], 7)), t0, t0),
		note(40, other, withMarker(line(hexA, 4)), t0, t0),
	}

	got, ok := SelectPrior(notes, bot.ID)
	if !ok {
		t.Fatalf("SelectPrior ok=false, want state of note 30")
	}
	if want := (State{SHA: hexA, Base: hexB, Mode: ModeUpdate, Round: 2}); got != want {
		t.Errorf("SelectPrior = %+v, want %+v", got, want)
	}

	for _, s := range []State{
		{SHA: hexA, Base: hexB, Mode: ModeFull, Round: 0},
		{SHA: hexA, Base: hexB, Mode: ModeUpdate, Round: 1},
		{SHA: hexA, Base: hexB, Mode: ModeUpdate, Round: 2147483647},
	} {
		f := FormatState(s)
		if !strings.HasPrefix(f, "<!-- mrinspect:state ") || !strings.HasSuffix(f, " -->") || strings.Contains(f, "\n") {
			t.Errorf("FormatState(%+v) = %q, not the one-line state comment", s, f)
		}
		if p, ok := ParseState(f); !ok || p != s {
			t.Errorf("ParseState(FormatState(%+v)) = %+v, %v", s, p, ok)
		}
	}

	mk := func(sha, mode, round string) string {
		return "<!-- mrinspect:state sha=" + sha + " base=" + hexB + " mode=" + mode + " round=" + round + " -->"
	}
	for name, bad := range map[string]string{
		"39-hex sha":     mk(hexA[:39], "full", "0"),
		"uppercase hex":  mk(strings.ToUpper(hexA), "full", "0"),
		"negative round": mk(hexA, "full", "-1"),
		"round > int32":  mk(hexA, "full", "2147483648"),
		"unknown mode":   mk(hexA, "partial", "0"),
	} {
		if p, ok := ParseState(bad); ok {
			t.Errorf("ParseState accepted %s: %q -> %+v", name, bad, p)
		}
	}
}

// REQ-04 / S-04
func TestDecide_Table(t *testing.T) {
	prior := func(round int) *State {
		return &State{SHA: hexA, Base: hexB, Mode: ModeUpdate, Round: round}
	}
	const other = "cccccccccccccccccccccccccccccccccccccccc"
	rows := []struct {
		name      string
		prior     *State
		head      string
		base      string
		fullEvery int
		ancestor  bool
		want      Decision
		wantCalls int
	}{
		{"1 no prior", nil, hexA, hexB, 5, true, Decision{Mode: ModeFull, Round: 0}, 0},
		{"2 same head and base", prior(3), hexA, hexB, 5, true, Decision{Skip: true}, 0},
		{"3 base differs", prior(3), hexA, other, 5, true, Decision{Mode: ModeFull, Round: 0}, 0},
		{"4 cap reached", prior(5), other, hexB, 5, true, Decision{Mode: ModeFull, Round: 0}, 0},
		{"5 not ancestor", prior(3), other, hexB, 5, false, Decision{Mode: ModeFull, Round: 0}, 1},
		{"6 ancestor", prior(3), other, hexB, 5, true, Decision{Mode: ModeUpdate, Round: 4}, 1},
		{"7 FullEvery=0 never caps", prior(99), other, hexB, 0, true, Decision{Mode: ModeUpdate, Round: 100}, 1},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			calls := 0
			got := Decide(r.prior, r.head, r.base, r.fullEvery, func() bool { calls++; return r.ancestor })
			if got != r.want {
				t.Errorf("Decide = %+v, want %+v", got, r.want)
			}
			if calls != r.wantCalls {
				t.Errorf("isAncestor calls = %d, want %d", calls, r.wantCalls)
			}
		})
	}
}
