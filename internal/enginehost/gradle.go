package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// gradleHomeName is the directory under the state directory that GradleEnviron makes the GRADLE_USER_HOME of every child
// a holder runs.
const gradleHomeName = "gradle"

// gradleHomeVar names Gradle's user home in the environment.
const gradleHomeVar = "GRADLE_USER_HOME"

// gradleProperties turns off the Gradle daemon, runs the Kotlin compiler inside the build, and gives each build at most
// eight workers. A daemon serves every later build of the user that can reach it, so a build in one step could run in a
// daemon another step started, and a holder's scope is stopped when the holder ends, which would end that daemon under
// the other step's build. Gradle's own worker count is every core, so the several builds a host's steps and gates run
// at once would each take all of them. Gradle reads these properties from GRADLE_USER_HOME ahead of a project's own, and
// the environment's GRADLE_OPTS is no place for them: a project's dev shell can replace it.
const gradleProperties = "org.gradle.daemon=false\nkotlin.compiler.execution.strategy=in-process\norg.gradle.workers.max=8\n"

// gradleHomeMode is the mode of the Gradle home GradleEnviron makes: this user's only.
const gradleHomeMode = 0o700

// gradlePropertiesMode is the mode of the gradle.properties GradleEnviron writes, and of its build lock script.
const gradlePropertiesMode = 0o600

// gradleInitDir is the directory of a Gradle home whose scripts Gradle runs before every build.
const gradleInitDir = "init.d"

// gradleBuildLockScriptName names the build lock script GradleEnviron writes in the home's gradleInitDir.
const gradleBuildLockScriptName = "orrery-build-lock.gradle"

// gradleBuildLockScript has the builds that share the Gradle home take turns. One Android build takes every core it is
// given and several gigabytes, so the gates, evidence captures and node checks that run Gradle on one host at once, as
// they do when they fall back from a gate box, would take the host down between them. The lock lives here, not in the
// project, because those runs build whatever commit they check out, and an older commit carries no lock of its own. A
// build holds an exclusive lock on orrery-build.lock in the home from before it reads its settings until its process
// ends, when the kernel lets the lock go; a build that finds it held says so and waits. Every orreryd build runs in a
// process of its own, as the home's gradle.properties turns the daemon off, and a nested build in the same process
// (buildSrc, an included build) finds the lock already held by its process and takes nothing. The shutdown hook keeps
// the channel, and with it the lock, reachable until the process ends: a channel the collector closed would let the
// lock go mid-build.
const gradleBuildLockScript = `import java.nio.channels.FileChannel
import java.nio.file.StandardOpenOption

if (System.getProperty('orrery.buildLock') == null) {
    def file = new File(gradle.gradleUserHomeDir, 'orrery-build.lock')
    def channel = FileChannel.open(file.toPath(), StandardOpenOption.CREATE, StandardOpenOption.WRITE)
    if (channel.tryLock() == null) {
        logger.warn("orrery: waiting for another Gradle build on this host (${file})")
        channel.lock()
    }
    Runtime.runtime.addShutdownHook(new Thread({ channel.close() }))
    System.setProperty('orrery.buildLock', 'held')
}
`

// gradleShared returns the entries of the user's own Gradle home that GradleEnviron's home links to, so builds share the
// dependency caches, native libraries, wrapper distributions and Android files the user already has. Gradle locks the
// caches for use by many builds at once.
func gradleShared() []string {
	return []string{"caches", "native", "wrapper", "android"}
}

// GradleEnviron returns environ with GRADLE_USER_HOME set to the gradle directory under stateDir, for the children a holder
// runs: sessions, steps and gates. It writes that home's gradle.properties as gradleProperties gives, so no build a child
// runs starts a daemon another child's build could reach, and its init.d/orrery-build-lock.gradle as
// gradleBuildLockScript gives, so the builds take turns. The home links each gradleShared entry of userHome, the user's
// own Gradle home (UserGradleHome), when that entry exists and the home has none yet; an empty userHome links nothing.
// Every GRADLE_USER_HOME environ held is replaced. The home is made, and its files written and linked, through files,
// the file system of the Host the home is made on.
func GradleEnviron(
	ctx context.Context, files hostexec.Files, environ []string, stateDir, userHome string,
) ([]string, error) {
	home := filepath.Join(stateDir, gradleHomeName)
	if err := files.MkdirAll(ctx, home, gradleHomeMode); err != nil {
		return nil, fmt.Errorf("make gradle home: %w", err)
	}
	if err := files.WriteFile(ctx, filepath.Join(home, "gradle.properties"), []byte(gradleProperties),
		gradlePropertiesMode); err != nil {
		return nil, fmt.Errorf("write gradle properties: %w", err)
	}
	initDir := filepath.Join(home, gradleInitDir)
	if err := files.MkdirAll(ctx, initDir, gradleHomeMode); err != nil {
		return nil, fmt.Errorf("make gradle init dir: %w", err)
	}
	if err := files.WriteFile(ctx, filepath.Join(initDir, gradleBuildLockScriptName), []byte(gradleBuildLockScript),
		gradlePropertiesMode); err != nil {
		return nil, fmt.Errorf("write gradle build lock script: %w", err)
	}
	if userHome != "" {
		if err := linkGradleShared(ctx, files, home, userHome); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if !strings.HasPrefix(entry, gradleHomeVar+"=") {
			out = append(out, entry)
		}
	}
	return append(out, gradleHomeVar+"="+home), nil
}

// UserGradleHome returns the user's own Gradle home as getenv names it: GRADLE_USER_HOME, else HOME/.gradle, else ""
// when it names neither.
func UserGradleHome(getenv func(string) string) string {
	if home := getenv(gradleHomeVar); home != "" {
		return home
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".gradle")
	}
	return ""
}

// linkGradleShared links each gradleShared entry of user into home, when user has it and home has no entry by that name.
// It inspects and links through files.
func linkGradleShared(ctx context.Context, files hostexec.Files, home, user string) error {
	for _, name := range gradleShared() {
		target := filepath.Join(user, name)
		if _, err := files.Stat(ctx, target); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect gradle %s: %w", name, err)
		}
		link := filepath.Join(home, name)
		if _, err := files.Lstat(ctx, link); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect gradle home %s: %w", name, err)
		}
		if err := files.Symlink(ctx, target, link); err != nil {
			return fmt.Errorf("link gradle %s: %w", name, err)
		}
	}
	return nil
}
