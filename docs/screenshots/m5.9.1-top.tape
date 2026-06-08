Output ../../docs/screenshots/m5.9.1-skoed-top.gif

Set Theme "Catppuccin Mocha"
Set FontSize 16
Set Width 1000
Set Height 760
Set Padding 24
Set TypingSpeed 60ms

Hide
Type "export SKOED_AUTH=admin:demopass123"
Enter
Type "export SKOED_API=http://127.0.0.1:18829"
Enter
Type "alias skoed='/home/jcollin/repos/skoed/apps/skoed/skoed'"
Enter
Type "clear"
Enter
Show

Type "skoed top"
Sleep 200ms
Enter
# Let the TUI render and refresh a couple of times.
Sleep 6s
# Press 'r' for a manual refresh — shows the hot-key in action.
Type "r"
Sleep 3s
# Quit.
Type "q"
Sleep 500ms
