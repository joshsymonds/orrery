# VHS tape for docs/img/tree.png, driven by `just tui-shots`.
#
# The shell runs the real `orrery tree` built from this tree against the
# private tmux server that docs/tui/fixture.sh start builds. The command is
# typed hidden and `clear` removes the prompt, so the frame holds only the
# tree. Output lands in the recipe's temporary directory, which is the working
# directory VHS runs from, so the gif never enters the repository.
Output tree.gif
Set FontFamily "Maple Mono NF"
Set FontSize 14
Set Theme "Catppuccin Mocha"
Set Width 1000
Set Height 540
Set Padding 12
Hide
Type "clear && orrery -S /tmp/orrery-tui-shots/tmux.sock tree --client /dev/pts/99"
Enter
Show
Sleep 3s
