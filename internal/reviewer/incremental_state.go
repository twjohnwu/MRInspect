package reviewer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mrinspect/internal/gitlab"
)

// ReviewMode is the mode recorded in a state comment.
type ReviewMode string

const (
	ModeFull   ReviewMode = "full"
	ModeUpdate ReviewMode = "update"
)

// State is the review state carried by a state comment.
type State struct {
	SHA, Base string
	Mode      ReviewMode
	Round     int
}

// Decision is the outcome of Decide.
type Decision struct {
	Skip  bool
	Mode  ReviewMode
	Round int
}

// stateLineRe matches the whole state comment line.
var stateLineRe = regexp.MustCompile(
	`^<!-- mrinspect:state sha=([0-9a-f]{40}) base=([0-9a-f]{40}) mode=(full|update) round=(0|[1-9][0-9]*) -->$`)

// FormatState renders the one-line state comment.
func FormatState(s State) string {
	return fmt.Sprintf("<!-- mrinspect:state sha=%s base=%s mode=%s round=%d -->",
		s.SHA, s.Base, s.Mode, s.Round)
}

// ParseState strictly parses a state comment line.
func ParseState(line string) (State, bool) {
	m := stateLineRe.FindStringSubmatch(line)
	if m == nil {
		return State{}, false
	}
	round, err := strconv.ParseInt(m[4], 10, 32)
	if err != nil {
		return State{}, false
	}
	return State{SHA: m[1], Base: m[2], Mode: ReviewMode(m[3]), Round: int(round)}, true
}

// SelectPrior returns the prior state from the bot's own unedited notes.
func SelectPrior(notes []gitlab.Note, botID int) (State, bool) {
	if botID == 0 {
		return State{}, false
	}
	var best State
	bestID, found := 0, false
	for _, n := range notes {
		if n.Author.ID != botID || n.CreatedAt != n.UpdatedAt {
			continue
		}
		lines := strings.Split(n.Body, "\n")
		if strings.TrimRight(lines[0], "\r") != ReviewNoteMarker {
			continue
		}
		for _, l := range lines {
			st, ok := ParseState(strings.TrimRight(l, "\r"))
			if !ok {
				continue
			}
			if !found || n.ID > bestID {
				best, bestID, found = st, n.ID, true
			}
			break
		}
	}
	return best, found
}

// Decide applies the decision table; isAncestor is called lazily.
func Decide(prior *State, head, base string, fullEvery int, isAncestor func() bool) Decision {
	full := Decision{Mode: ModeFull}
	switch {
	case prior == nil:
		return full
	case head == prior.SHA && base == prior.Base:
		return Decision{Skip: true}
	case base != prior.Base:
		return full
	case fullEvery > 0 && prior.Round >= fullEvery:
		return full
	case !isAncestor():
		return full
	}
	return Decision{Mode: ModeUpdate, Round: prior.Round + 1}
}
