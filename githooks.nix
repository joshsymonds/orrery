{ ... }:

# Points git at the checked-in .githooks directory. The value lands in the
# clone's shared config, so every worktree of the clone reads it. It is written
# only when it differs, so loading the environment again leaves that file
# untouched. Outside a work tree there is nothing to configure.
{
  enterShell = ''
    if [ "$(git rev-parse --is-inside-work-tree 2>/dev/null)" = true ] &&
       [ "$(git config --local --get core.hooksPath 2>/dev/null)" != ".githooks" ]; then
      git config --local core.hooksPath .githooks
    fi
  '';
}
