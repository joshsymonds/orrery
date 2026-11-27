package releasestep

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/hosts"
)

// The facts a hosts step names when the controller's hosts file lists no host, and when a host does not answer.
const (
	factNoHosts = "no hosts.toml hosts: nothing to switch"
	factSkipped = "host %s did not answer: %v; skipped, upgraded on the next release"
)

// switchHosts is a hosts step. It switches each host the controller's hosts file lists to the candidate's build: it
// runs its argv in the flake repository once for each host that answers, and checks that each unit of each answering
// host runs a build whose commit contains the candidate.
type switchHosts struct {
	s         *step
	candidate string
	flake     string
	units     []string
	argv      []string
	// answering are the hosts that answered the last check, which act switches.
	answering []remoteHost
}

// remoteHost is a host of the hosts file as a hosts step reaches it: its name, and the target ssh and the switch name,
// which is the host part of its address.
type remoteHost struct {
	name   string
	target string
}

// check probes each host the hosts file lists. It holds when every host that answers runs, in each of its units, a
// build whose commit contains the candidate. A host that does not answer is skipped and named in a fact. With no hosts
// file, or a file that lists no host, it holds and nothing runs.
func (h *switchHosts) check(ctx context.Context) (verdict, error) {
	file, err := hosts.Load(h.s.getenv)
	if err != nil {
		return verdict{}, fmt.Errorf("list the controller's hosts: %w", err)
	}
	if len(file.Hosts) == 0 {
		return verdict{held: true, facts: []string{factNoHosts}}, nil
	}
	h.answering = nil
	var facts, whys []string
	for _, entry := range file.Hosts {
		hostFacts, hostWhys, probeErr := h.probeHost(ctx, entry)
		if probeErr != nil {
			return verdict{}, probeErr
		}
		facts = append(facts, hostFacts...)
		whys = append(whys, hostWhys...)
	}
	if len(whys) > 0 {
		return verdict{facts: facts, why: strings.Join(whys, "; ")}, nil
	}
	return verdict{held: true, facts: facts}, nil
}

// probeHost probes the host of the hosts file entry. A host that does not answer is skipped and named in a fact. One
// that answers is recorded for act, and its units are checked.
func (h *switchHosts) probeHost(ctx context.Context, entry hosts.Host) ([]string, []string, error) {
	target, err := hostTarget(entry.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("host %s: %w", entry.Name, err)
	}
	host := remoteHost{name: entry.Name, target: target}
	if err = h.s.machine.Run(ctx, h.s.repo, append(sshArgv(target), "true"), io.Discard, h.s.stderr); err != nil {
		return []string{fmt.Sprintf(factSkipped, host.name, err)}, nil, nil
	}
	h.answering = append(h.answering, host)
	return h.checkHost(ctx, host)
}

// checkHost reads each unit of host over ssh. It returns a fact that names each unit, and a why for each unit that does
// not meet the postcondition.
func (h *switchHosts) checkHost(ctx context.Context, host remoteHost) ([]string, []string, error) {
	var facts, whys []string
	for _, unit := range h.units {
		fact, why, err := h.checkUnit(ctx, host.target, unit)
		if err != nil {
			return nil, nil, fmt.Errorf("host %s: %w", host.name, err)
		}
		facts = append(facts, fmt.Sprintf("host %s: %s", host.name, fact))
		if why != "" {
			whys = append(whys, fmt.Sprintf("host %s: %s", host.name, why))
		}
	}
	return facts, whys, nil
}

// checkUnit reads unit on the host at target over ssh: its systemd properties, the exe of its main process, and the
// commit file of that exe's build. It returns the fact that names them, and why the unit does not meet the
// postcondition, or "" when it does. The commit is tested for containing the candidate as update's check tests it.
func (h *switchHosts) checkUnit(ctx context.Context, target, unit string) (fact, why string, err error) {
	out, err := h.remote(ctx, target, "systemctl", "--user", "show", "--property="+propActiveState,
		"--property="+propMainPID, unit)
	if err != nil {
		return "", "", fmt.Errorf("read unit %s: %w", unit, err)
	}
	props := unitProperties(out)
	state := props[propActiveState]
	pid := mainPID(props[propMainPID])
	if state != stateActive || pid <= 0 {
		return fmt.Sprintf("unit %s is %s with main pid %s", unit, state, props[propMainPID]),
			fmt.Sprintf("unit %s is not active with a main process", unit), nil
	}
	exe, err := h.remote(ctx, target, "readlink", fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return fmt.Sprintf("unit %s is active, pid %d, exe unreadable: %v", unit, pid, err),
			fmt.Sprintf("the exe of unit %s cannot be read", unit), nil
	}
	fact = fmt.Sprintf("unit %s is active, pid %d, exe %s", unit, pid, exe)
	commit, why := h.buildCommit(ctx, target, exe)
	if why != "" {
		return fact, fmt.Sprintf("unit %s: %s", unit, why), nil
	}
	fact += ", built from " + commit
	u := &update{s: h.s, candidate: h.candidate}
	why, err = u.contains(ctx, commit)
	if err != nil {
		return "", "", err
	}
	if why != "" {
		return fact, fmt.Sprintf("unit %s: %s", unit, why), nil
	}
	return fact + ", which contains candidate " + h.candidate, "", nil
}

// buildCommit reads, over ssh, the commit file of the build exe is in, two directories above it, and returns the
// commit it holds, or why it holds none.
func (h *switchHosts) buildCommit(ctx context.Context, target, exe string) (commit, why string) {
	path := filepath.Join(filepath.Dir(filepath.Dir(exe)), BuildCommitFile)
	text, err := h.remote(ctx, target, "cat", path)
	if err != nil {
		return "", fmt.Sprintf("its build records no commit: %v", err)
	}
	if !commitPattern.MatchString(text) {
		return "", fmt.Sprintf("%s holds %q, not a full commit id", path, text)
	}
	return text, ""
}

// remote runs words on the host at target over ssh, and returns what it prints, trimmed.
func (h *switchHosts) remote(ctx context.Context, target string, words ...string) (string, error) {
	var out bytes.Buffer
	if err := h.s.machine.Run(ctx, h.s.repo, sshArgv(target, words...), &out, h.s.stderr); err != nil {
		return "", fmt.Errorf("on %s: %w", target, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// act runs the argv in the flake repository once for each host that answered the check, with {host} and {target}
// replaced by the host's name and target. The command's output goes to stderr, and a run that fails stops the step.
func (h *switchHosts) act(ctx context.Context) error {
	for _, host := range h.answering {
		argv := hostArgv(h.argv, host)
		if err := h.s.machine.Run(ctx, h.flake, argv, h.s.stderr, h.s.stderr); err != nil {
			return fmt.Errorf("switch host %s: %w", host.name, err)
		}
		h.s.say(fmt.Sprintf("switched host %s: ran %s in %s", host.name, strings.Join(argv, " "), h.flake))
	}
	return nil
}

// hostArgv returns argv with {host} replaced by the host's name, and {target} by its target.
func hostArgv(argv []string, host remoteHost) []string {
	replacer := strings.NewReplacer("{host}", host.name, "{target}", host.target)
	out := make([]string, 0, len(argv))
	for _, word := range argv {
		out = append(out, replacer.Replace(word))
	}
	return out
}

// sshArgv returns the argv that runs words on target over ssh, in batch mode with a ten second connect timeout, so a
// host that does not answer is skipped without a long wait or a prompt. Each word is quoted for the remote shell, so a
// word the host named, such as an exe path, reaches its command as it is.
func sshArgv(target string, words ...string) []string {
	argv := make([]string, 0, 6+len(words))
	argv = append(argv, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target)
	for _, word := range words {
		argv = append(argv, "'"+strings.ReplaceAll(word, "'", `'\''`)+"'")
	}
	return argv
}

// hostTarget returns the host part of address, the name ssh and the switch reach the host by. A host part that starts
// with a dash would be read as an option of ssh, so it is refused.
func hostTarget(address string) (string, error) {
	target, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("address %q: %w", address, err)
	}
	if strings.HasPrefix(target, "-") {
		return "", fmt.Errorf("address %q: host %q starts with a dash", address, target)
	}
	return target, nil
}
