package reviewer

import (
	"testing"

	"mrinspect/internal/gitlab"
)

// REQ-03 / S-03: identity and max-note-ID edge cases.
func TestState_SelectPriorEdgeCases(t *testing.T) {
	const t0 = "2026-10-01T00:00:00Z"
	bot := gitlab.Author{ID: 7, Username: "mrinspect-bot"}
	line := func(round int) string {
		return FormatState(State{SHA: hexA, Base: hexB, Mode: ModeUpdate, Round: round})
	}
	note := func(id int, a gitlab.Author, body string) gitlab.Note {
		return gitlab.Note{ID: id, Author: a, Body: body, CreatedAt: t0, UpdatedAt: t0}
	}

	t.Run("max note ID wins regardless of slice order", func(t *testing.T) {
		notes := []gitlab.Note{
			note(30, bot, ReviewNoteMarker+"\n"+line(2)),
			note(10, bot, ReviewNoteMarker+"\n"+line(0)),
		}
		got, ok := SelectPrior(notes, bot.ID)
		if !ok || got.Round != 2 {
			t.Errorf("SelectPrior = %+v, %v; want round 2 (note 30)", got, ok)
		}
	})

	t.Run("marker must be line 1", func(t *testing.T) {
		for name, body := range map[string]string{
			"marker on line 2": "intro\n" + ReviewNoteMarker + "\n" + line(9),
			"marker absent":    line(9),
		} {
			notes := []gitlab.Note{
				note(20, bot, ReviewNoteMarker+"\n"+line(3)),
				note(99, bot, body),
			}
			got, ok := SelectPrior(notes, bot.ID)
			if !ok || got.Round != 3 {
				t.Errorf("%s: SelectPrior = %+v, %v; want round 3 (note 20)", name, got, ok)
			}
		}
	})

	t.Run("botID 0 never matches", func(t *testing.T) {
		notes := []gitlab.Note{note(5, gitlab.Author{ID: 0}, ReviewNoteMarker+"\n"+line(1))}
		if got, ok := SelectPrior(notes, 0); ok {
			t.Errorf("SelectPrior(botID=0) = %+v, true; want ok=false", got)
		}
	})

	t.Run("ParseState rejects leading-zero round and trailing space", func(t *testing.T) {
		base := "<!-- mrinspect:state sha=" + hexA + " base=" + hexB + " mode=full round="
		for name, bad := range map[string]string{
			"round=01":       base + "01 -->",
			"trailing space": base + "0 --> ",
		} {
			if p, ok := ParseState(bad); ok {
				t.Errorf("ParseState accepted %s: %q -> %+v", name, bad, p)
			}
		}
	})
}

// REQ-04 / S-04 row 2: same head and base skips even when the cap is reached.
func TestDecide_SkipBeatsCap(t *testing.T) {
	prior := &State{SHA: hexA, Base: hexB, Mode: ModeUpdate, Round: 5}
	calls := 0
	got := Decide(prior, hexA, hexB, 5, func() bool { calls++; return true })
	if !got.Skip {
		t.Errorf("Decide = %+v, want Skip=true", got)
	}
	if calls != 0 {
		t.Errorf("isAncestor calls = %d, want 0", calls)
	}
}
