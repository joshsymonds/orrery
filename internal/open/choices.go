package open

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// newWord is the first word of `new NAME`, which Choices sends to the intent lookup as text.
const newWord = "new"

// Choices returns the actions `orrery open TEXT` would offer for text, and executes none of them. It serves a caller
// with no current directory, so "." and "new NAME" are plain text for the intent lookup and no folder or session is
// created. One word that is a path, containing "/" or equal to "..", resolves as Resolve does with no current
// directory: an absolute existing directory gives the actions for that directory, and a relative or missing path is
// an error, never text. Otherwise the words decide as openChoice does: one word that names a session, or is a fast
// match with one, gives a jump to that session's active pane; a fast match with no session gives its resume actions;
// any other text gives the intent actions for its words joined by single spaces.
func Choices(ctx context.Context, cfg Config, text string) ([]Action, error) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil, errors.New("open: text is empty")
	}
	joined := strings.Join(words, " ")
	if len(words) > 1 || words[0] == newWord || words[0] == "." {
		return Intent(ctx, cfg, joined)
	}
	var plan Plan
	var err error
	if isPathWord(words[0]) {
		plan, err = resolvePath(ctx, cfg, Request{}, words[0])
	} else {
		plan, err = resolveWord(ctx, cfg, words[0])
	}
	if err != nil {
		return nil, err
	}
	switch {
	case plan.Dir != "":
		return ResumeActions(cfg, plan.Dir)
	case plan.Session != "":
		return sessionJump(ctx, cfg, plan.Session)
	}
	return Intent(ctx, cfg, joined)
}

// sessionJump returns the one jump action to the active pane of session name.
func sessionJump(ctx context.Context, cfg Config, name string) ([]Action, error) {
	out, err := cfg.Tmux.Run(ctx, "display-message", "-p", "-t", "="+name+":", "#{pane_id}")
	if err != nil {
		return nil, fmt.Errorf("open: session %q pane: %w", name, err)
	}
	pane := strings.TrimSpace(out)
	if pane == "" {
		return nil, fmt.Errorf("open: session %q has no active pane", name)
	}
	return []Action{{Kind: ActionJump, Pane: pane}}, nil
}
