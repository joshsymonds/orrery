package enginehost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// gradleHomeName is the directory under the state directory that GradleEnviron makes the GRADLE_USER_HOME of every child
// a holder runs.
const gradleHomeName = "gradle"

// gradleHomeVar names Gradle's user home in the environment.
const gradleHomeVar = "GRADLE_USER_HOME"

// gradleProperties turns off the Gradle daemon and runs the Kotlin compiler inside the build. A daemon serves every later
// build of the user that can reach it, so a build in one step could run in a daemon another step started, and a holder's
// scope is stopped when the holder ends, which would end that daemon under the other step's build. Gradle reads these
// properties from GRADLE_USER_HOME ahead of a project's own, and the environment's GRADLE_OPTS is no place for them: a
// project's dev shell can replace it.
const gradleProperties = "org.gradle.daemon=false\nkotlin.compiler.execution.strategy=in-process\n"

// gradleHomeMode is the mode of the Gradle home GradleEnviron makes: this user's only.
const gradleHomeMode = 0o700

// gradlePropertiesMode is the mode of the gradle.properties GradleEnviron writes.
const gradlePropertiesMode = 0o600

// gradleShared returns the entries of the user's own Gradle home that GradleEnviron's home links to, so builds share the
// dependency caches, native libraries, wrapper distributions and Android files the user already has. Gradle locks the
// caches for use by many builds at once.
func gradleShared() []string {
	return []string{"caches", "native", "wrapper", "android"}
}

// GradleEnviron returns environ with GRADLE_USER_HOME set to the gradle directory under stateDir, for the children a holder
// runs: sessions, steps and gates. It writes that home's gradle.properties as gradleProperties gives, so no build a child
// runs starts a daemon another child's build could reach. The home links each gradleShared entry of userHome, the user's
// own Gradle home (UserGradleHome), when that entry exists and the home has none yet; an empty userHome links nothing.
// Every GRADLE_USER_HOME environ held is replaced.
func GradleEnviron(environ []string, stateDir, userHome string) ([]string, error) {
	home := filepath.Join(stateDir, gradleHomeName)
	if err := os.MkdirAll(home, gradleHomeMode); err != nil {
		return nil, fmt.Errorf("make gradle home: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "gradle.properties"), []byte(gradleProperties),
		gradlePropertiesMode); err != nil {
		return nil, fmt.Errorf("write gradle properties: %w", err)
	}
	if userHome != "" {
		if err := linkGradleShared(home, userHome); err != nil {
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
func linkGradleShared(home, user string) error {
	for _, name := range gradleShared() {
		target := filepath.Join(user, name)
		if _, err := os.Stat(target); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect gradle %s: %w", name, err)
		}
		link := filepath.Join(home, name)
		if _, err := os.Lstat(link); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect gradle home %s: %w", name, err)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link gradle %s: %w", name, err)
		}
	}
	return nil
}
