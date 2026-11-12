package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// defaultControlTimeout bounds each Control forward when Config.ControlTimeout is zero.
const defaultControlTimeout = 10 * time.Second

// maxControlBody is the largest Control request body the handler reads.
const maxControlBody = 1 << 20

// controlUnavailable is the fixed message for an orreryd that is away or has no store.
const controlUnavailable = "session service unavailable"

// controlFailed is the fixed message for an orreryd internal or unknown failure.
const controlFailed = "session command failed"

// controlTimedOut is the fixed message for a forward that outlives the control timeout.
const controlTimedOut = "session command timed out"

// controlStartRefused is the fixed message for a start whose directory is not an existing directory under the home.
const controlStartRefused = "session directory must be an existing directory under the home directory"

// controlBelowStamp is the message for a start in a folder with no stamp of its own, below the stamped folder named by
// its %s.
const controlBelowStamp = "session directory has no context stamp of its own and is below a context-stamped folder (%s)"

// controlWorktreeStamp is the message for a start in a folder with no stamp of its own, in a linked git worktree whose
// checkout is at or below the stamped folder named by its %s.
const controlWorktreeStamp = "session directory has no context stamp of its own and is a linked git worktree of a checkout at or below a context-stamped folder (%s)"

// controlGitUnreadable is the message for a start whose git file or common directory pointer exists but cannot be
// read; its %s is the file's path.
const controlGitUnreadable = "session directory's git file %s cannot be read"

// controlStampUnreadable is the message for a start whose stamp settings file exists but cannot be read; its %s is the
// file's path.
const controlStampUnreadable = "session directory's context stamp %s cannot be read"

// controlServer answers orrery.v1.Control by forwarding each call to engine. A nil engine answers unavailable. Start
// forwards only a directory under home that refuseUnstampedBelowStamp admits.
type controlServer struct {
	engine  enginev1connect.EngineClient
	timeout time.Duration
	home    string
}

// Start forwards a start to the engine and answers with the started session's id. The directory forwarded is the one
// resolveSessionDir admits, with its symlinks resolved, and only when refuseUnstampedBelowStamp admits it too.
func (c controlServer) Start(
	ctx context.Context,
	req *connect.Request[orreryv1.StartRequest],
) (*connect.Response[orreryv1.StartResponse], error) {
	dir, dirErr := resolveSessionDir(c.home, req.Msg.GetDir())
	if dirErr != nil {
		return nil, dirErr
	}
	if stampErr := refuseUnstampedBelowStamp(c.home, dir); stampErr != nil {
		return nil, stampErr
	}
	var id string
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		resp, err := engine.Start(ctx, connect.NewRequest(&enginev1.StartRequest{
			Dir:    dir,
			Prompt: req.Msg.GetPrompt(),
			Model:  req.Msg.GetModel(),
			Id:     req.Msg.GetId(),
		}))
		if err != nil {
			return wrapEngine("start", err)
		}
		id = resp.Msg.GetId()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StartResponse{Id: id}), nil
}

// Say forwards a message to the engine.
func (c controlServer) Say(
	ctx context.Context,
	req *connect.Request[orreryv1.SayRequest],
) (*connect.Response[orreryv1.SayResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Say(ctx, connect.NewRequest(&enginev1.SayRequest{
			Id:       req.Msg.GetId(),
			Text:     req.Msg.GetText(),
			PromptId: req.Msg.GetPromptId(),
		}))
		return wrapEngine("say", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.SayResponse{}), nil
}

// Answer forwards an answer to the engine.
func (c controlServer) Answer(
	ctx context.Context,
	req *connect.Request[orreryv1.AnswerRequest],
) (*connect.Response[orreryv1.AnswerResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Answer(ctx, connect.NewRequest(&enginev1.AnswerRequest{
			Id:        req.Msg.GetId(),
			RequestId: req.Msg.GetRequestId(),
			Answer:    req.Msg.GetAnswer(),
		}))
		return wrapEngine("answer", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.AnswerResponse{}), nil
}

// Interrupt forwards an interrupt to the engine.
func (c controlServer) Interrupt(
	ctx context.Context,
	req *connect.Request[orreryv1.InterruptRequest],
) (*connect.Response[orreryv1.InterruptResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Interrupt(ctx, connect.NewRequest(&enginev1.InterruptRequest{Id: req.Msg.GetId()}))
		return wrapEngine("interrupt", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.InterruptResponse{}), nil
}

// Stop forwards a stop to the engine.
func (c controlServer) Stop(
	ctx context.Context,
	req *connect.Request[orreryv1.StopRequest],
) (*connect.Response[orreryv1.StopResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Stop(ctx, connect.NewRequest(&enginev1.StopRequest{Id: req.Msg.GetId()}))
		return wrapEngine("stop", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StopResponse{}), nil
}

// Seen forwards a seen mark to the engine.
func (c controlServer) Seen(
	ctx context.Context,
	req *connect.Request[orreryv1.SeenRequest],
) (*connect.Response[orreryv1.SeenResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Seen(ctx, connect.NewRequest(&enginev1.SeenRequest{Id: req.Msg.GetId()}))
		return wrapEngine("seen", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.SeenResponse{}), nil
}

// forward runs call against the engine under the control timeout. A nil engine answers unavailable. Any engine
// error answers as translateError does.
func (c controlServer) forward(
	ctx context.Context,
	call func(context.Context, enginev1connect.EngineClient) error,
) error {
	if c.engine == nil {
		return listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := call(ctx, c.engine); err != nil {
		return translateError(err)
	}
	return nil
}

// resolveSessionDir returns dir with its symlinks resolved when dir is an absolute path to an existing directory equal
// to or under home, with its symlinks resolved. Any other dir answers invalid_argument, and so does any home that is
// empty, relative, the root directory, or missing.
func resolveSessionDir(home, dir string) (string, error) {
	refused := listError(connect.CodeInvalidArgument, controlStartRefused)
	root, ok := resolveHome(home)
	if !ok || !filepath.IsAbs(dir) {
		return "", refused
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", refused
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", refused
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", refused
	}
	return resolved, nil
}

// resolveHome returns home with its symlinks resolved. It reports ok false for an empty or relative home, for the
// root directory, and for a home that does not resolve.
func resolveHome(home string) (string, bool) {
	if !filepath.IsAbs(home) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil || resolved == string(filepath.Separator) {
		return "", false
	}
	return resolved, true
}

// refuseUnstampedBelowStamp returns the refusal for a start in dir, a directory resolveSessionDir admitted under home,
// when dir has no context stamp of its own and either a folder between dir and home has one, or dir lies in a linked
// git worktree whose checkout is stamped or sits below a stamped folder. Home's own files never count, so a start in
// home, or in a folder with no stamped folder above it below home, is admitted.
func refuseUnstampedBelowStamp(home, dir string) error {
	root, ok := resolveHome(home)
	if !ok {
		return listError(connect.CodeInvalidArgument, controlStartRefused)
	}
	if dir == root {
		return nil
	}
	own, ownErr := folderStamped(dir)
	if ownErr != nil {
		return ownErr
	}
	if own {
		return nil
	}
	stamped, stampErr := stampedFrom(root, filepath.Dir(dir))
	if stampErr != nil {
		return stampErr
	}
	if stamped != "" {
		return listError(connect.CodeInvalidArgument, fmt.Sprintf(controlBelowStamp, stamped))
	}
	return refuseWorktreeBelowStamp(root, dir)
}

// stampedFrom returns the nearest of folder and the folders above it that is stamped, or "" when none is. The walk
// stops at root, which never counts, and a folder outside root is checked alone.
func stampedFrom(root, folder string) (string, error) {
	below := root + string(filepath.Separator)
	for current := folder; ; current = filepath.Dir(current) {
		if current == root {
			return "", nil
		}
		stamped, err := folderStamped(current)
		if err != nil {
			return "", err
		}
		if stamped {
			return current, nil
		}
		if !strings.HasPrefix(current, below) {
			return "", nil
		}
	}
}

// refuseWorktreeBelowStamp returns the refusal for a start in dir, a directory with no stamp of its own, when dir lies
// in a linked git worktree whose primary checkout is stamped or sits below a stamped folder under root.
func refuseWorktreeBelowStamp(root, dir string) error {
	primary, err := linkedWorktreePrimary(root, dir)
	if err != nil || primary == "" {
		return err
	}
	stamped, stampErr := stampedFrom(root, primary)
	if stampErr != nil || stamped == "" {
		return stampErr
	}
	return listError(connect.CodeInvalidArgument, fmt.Sprintf(controlWorktreeStamp, stamped))
}

// linkedWorktreePrimary returns the primary checkout of the linked worktree dir lies in. It looks for a .git entry in
// dir or a folder above it below root, and the nearest one decides: a .git directory is an ordinary checkout and ends
// the search with no worktree. It returns "" when no linked worktree is named.
func linkedWorktreePrimary(root, dir string) (string, error) {
	below := root + string(filepath.Separator)
	for folder := dir; strings.HasPrefix(folder, below); folder = filepath.Dir(folder) {
		entry := filepath.Join(folder, ".git")
		info, err := os.Lstat(entry)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return "", listError(connect.CodeInvalidArgument, fmt.Sprintf(controlGitUnreadable, entry))
		}
		if !info.Mode().IsRegular() {
			return "", nil
		}
		return worktreePrimary(entry)
	}
	return "", nil
}

// worktreePrimary returns the primary checkout of the linked worktree whose .git file is entry: the parent of the
// common git directory that the gitdir line names. A file that does not name a gitdir with a commondir names no
// worktree, and returns "".
func worktreePrimary(entry string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(entry))
	if err != nil {
		return "", listError(connect.CodeInvalidArgument, fmt.Sprintf(controlGitUnreadable, entry))
	}
	line, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	gitdir := strings.TrimSpace(line)
	if !ok || gitdir == "" {
		return "", nil
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(entry), gitdir)
	}
	common, err := fs.ReadFile(os.DirFS(gitdir), "commondir")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "", nil
	}
	if err != nil {
		commonPath := filepath.Join(gitdir, "commondir")
		return "", listError(connect.CodeInvalidArgument, fmt.Sprintf(controlGitUnreadable, commonPath))
	}
	shared := strings.TrimSpace(string(common))
	if shared == "" {
		return "", nil
	}
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(gitdir, shared)
	}
	return filepath.Dir(shared), nil
}

// folderStamped reports whether folder is stamped: either of its settings files, .claude/settings.json or
// .claude/settings.local.json, sets env.ANTHROPIC_BASE_URL to a non-empty string. A settings path that is not a file,
// whether it does not exist or a component above it is not a directory, is no stamp. A settings file that exists but
// cannot be read answers the refusal for it.
func folderStamped(folder string) (bool, error) {
	stamped := false
	for _, name := range []string{"settings.json", "settings.local.json"} {
		path := filepath.Join(folder, ".claude", name)
		data, err := os.ReadFile(filepath.Clean(path))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return false, listError(connect.CodeInvalidArgument, fmt.Sprintf(controlStampUnreadable, path))
		}
		stamped = stamped || settingsStamp(data)
	}
	return stamped, nil
}

// settingsStamp reports whether a settings file's data sets env.ANTHROPIC_BASE_URL to a non-empty string. Data that is
// not JSON, or whose env is not an object, is no stamp.
func settingsStamp(data []byte) bool {
	var settings map[string]json.RawMessage
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	var env map[string]json.RawMessage
	if json.Unmarshal(settings["env"], &env) != nil {
		return false
	}
	var baseURL string
	if json.Unmarshal(env["ANTHROPIC_BASE_URL"], &baseURL) != nil {
		return false
	}
	return baseURL != ""
}

// wrapEngine names the engine call op on err, keeping err reachable for translateError. A nil err stays nil.
func wrapEngine(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("server: control %s: %w", op, err)
}

// translateError returns the error a Control call answers with for err from the engine. A deadline_exceeded answers
// with the timeout message whichever side's deadline ended the call: the forward's own timer, or the deadline the
// client sent orreryd, which can reach the client first. unavailable, internal and unknown answer fixed messages,
// so orreryd's text never reaches the client. Other codes pass through with their messages.
func translateError(err error) error {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return listError(connect.CodeInternal, controlFailed)
	}
	switch connectErr.Code() {
	case connect.CodeDeadlineExceeded:
		return listError(connect.CodeDeadlineExceeded, controlTimedOut)
	case connect.CodeUnavailable:
		return listError(connect.CodeUnavailable, controlUnavailable)
	case connect.CodeInternal, connect.CodeUnknown:
		return listError(connect.CodeInternal, controlFailed)
	default:
		return listError(connectErr.Code(), connectErr.Message())
	}
}

// newControlHandler returns the path and handler that serve Control over engine, reading at most maxControlBody
// bytes of each request. A zero timeout is defaultControlTimeout. Start admits only directories under home that are
// stamped, or that sit under no stamped folder below home and in no linked worktree of a stamped checkout.
func newControlHandler(engine enginev1connect.EngineClient, timeout time.Duration, home string) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewControlHandler(
		controlServer{engine: engine, timeout: timeout, home: home},
		connect.WithReadMaxBytes(maxControlBody),
	)
}
