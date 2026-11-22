package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

// registryTitle names the Claude session registry in a registry notice's desk title and push project.
const registryTitle = "Claude session registry"

// firing is the state of a registry notice, which is the state alerts give a firing alert's notice.
const firing = "firing"

// registryNotice is the notice of the registry file at path, which the pane watch could not read or parse, with cause
// err. It is keyed as an alert named by the file's base name, and is firing. Its desk notice is titled with the
// registry and bodied with the file and the error, and its push carries the same summary and nothing else. It is built
// as alertNotice builds an alert's notice, and is never watched or judged.
func registryNotice(path string, err error) route.Notice {
	summary := fmt.Sprintf("could not read %s: %v", path, err)
	return route.Notice{
		Key:   notice.Key{Alert: "claude registry " + filepath.Base(path)},
		State: firing,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskstream.Notice{Title: registryTitle, Body: summary}, nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{Project: registryTitle, State: protocol.State(firing), Summary: summary}, nil
		},
	}
}
