// Package stage serves the Stage service: the contract stage's tools over a design's record, which orreryd answers on
// its private socket. Each RPC is implemented in its own file.
package stage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostrepo"
	"github.com/joshsymonds/orrery/internal/store"
)

// Config is what the Stage service reads: the store it keeps the stage's records in, the steps it starts, the sessions
// it starts to continue a design's conversation, the clock it stamps records with, the environment it reads, the Hosts
// that resolve host names, the orrery name of the controller's host and the roots of each remote host.
type Config struct {
	Store    *store.Store
	Steps    StepStarter
	Sessions SessionStarter
	Now      func() time.Time
	Getenv   func(string) string
	Hosts    hostexec.Hosts
	// Controller is the orrery name of the controller's host.
	Controller string
	// Roots lists each remote host's roots by its orrery name.
	Roots map[string]hostrepo.RootLister
}

// StepStarter starts a step. *enginehost.Steps satisfies it.
type StepStarter interface {
	Start(ctx context.Context, start enginehost.StepStart) (string, error)
}

// SessionStarter starts a session that continues a design's conversation, and stops a session it started. The
// enginehost Manager satisfies it.
type SessionStarter interface {
	Start(ctx context.Context, req enginehost.StartRequest) (string, error)
	Stop(ctx context.Context, id string) error
}

// Service answers the Stage service. The RPCs it does not implement answer unimplemented.
type Service struct {
	enginev1connect.UnimplementedStageHandler
	cfg Config
}

// NewService returns the Stage service over cfg.
func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

// stepHostOf returns the orrery name of the host the steps of record run on: the design's host, or the controller's
// when the record holds none, as a contract that reads as a design of one does.
func (s *Service) stepHostOf(record store.DesignRecord) string {
	if record.Host == "" {
		return s.cfg.Controller
	}
	return record.Host
}

// workspaceOn returns the path of the checkout that a step on host runs in, for the repository at base. On the
// controller the repository is its own workspace. On another host, the origin of the repository is read on the
// controller, then the host's checkout of that origin is fetched, or cloned under the host's first root, and must hold
// base when base is set. A failure is refused as a failed precondition that names host and carries git's error.
func (s *Service) workspaceOn(ctx context.Context, host, repository, base string) (string, error) {
	if host == s.cfg.Controller {
		return repository, nil
	}
	controller, err := s.hostNamed(s.cfg.Controller)
	if err != nil {
		return "", hostRefusal(host, err)
	}
	origin, err := hostrepo.OriginURL(ctx, controller, repository)
	if err != nil {
		return "", hostRefusal(host, err)
	}
	target, err := s.hostNamed(host)
	if err != nil {
		return "", hostRefusal(host, err)
	}
	roots, ok := s.cfg.Roots[host]
	if !ok {
		return "", hostRefusal(host, errors.New("no roots to hold a checkout"))
	}
	checkout, err := hostrepo.Ensure(ctx, target, roots, hostrepo.Request{
		Host: host, Origin: origin, Preferred: repository, Base: base,
	})
	if err != nil {
		return "", errors.Join(connect.NewError(connect.CodeFailedPrecondition, err))
	}
	return checkout, nil
}

// headOf returns the HEAD commit of the controller's repository at repository. A failure is returned unnamed, for the
// caller to name the host it was for.
func (s *Service) headOf(ctx context.Context, repository string) (string, error) {
	controller, err := s.hostNamed(s.cfg.Controller)
	if err != nil {
		return "", err
	}
	out, err := controller.Git(ctx, hostexec.GitCall{Dir: repository, Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		return "", fmt.Errorf("head of %s: %w", repository, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// hostNamed returns the Host named name on the Stage's Hosts. A Stage with no Hosts resolves no name.
func (s *Service) hostNamed(name string) (hostexec.Host, error) {
	if s.cfg.Hosts == nil {
		return nil, fmt.Errorf("no hosts to resolve %s", name)
	}
	return s.cfg.Hosts.Host(name)
}

// hostRefusal returns the refusal of a start that a host the design's steps run on cannot take: a failed precondition
// that names the host and carries err.
func hostRefusal(host string, err error) error {
	return errors.Join(connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("host %s: %w", host, err)))
}
