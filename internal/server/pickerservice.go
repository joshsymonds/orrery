package server

import (
	"net/http"
	"time"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// pickerServer answers orrery.v1.Picker with the configuration, token store, tmux client and clock the REST picker
// routes share. The embedded Unimplemented handler answers every method until its method lands.
type pickerServer struct {
	orreryv1connect.UnimplementedPickerHandler
	picker func() (open.Config, error)
	tokens *pickerTokens
	tmux   *tmuxctl.Client
	now    func() time.Time
}

// newPickerHandler returns the path and handler that serve Picker over the instances New shares with the REST picker
// routes.
func newPickerHandler(
	picker func() (open.Config, error),
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	now func() time.Time,
) (string, http.Handler) {
	return orreryv1connect.NewPickerHandler(pickerServer{picker: picker, tokens: tokens, tmux: tmux, now: now})
}
