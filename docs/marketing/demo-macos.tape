# cc-clip macOS Demo

Output docs/marketing/demo-macos.gif
Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 900
Set Height 500
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set PlaybackSpeed 0.5

Hide
Type "export PS1='$ '"
Enter
Type "SIM=docs/marketing/sim-macos.sh"
Enter
Type "alias curl='bash $SIM install #'"
Enter
Type "alias cc-clip='bash $SIM setup #'"
Enter
Type "alias ssh='bash $SIM ssh #'"
Enter
Type "clear"
Enter
Sleep 500ms
Show

Sleep 500ms

Type "curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh"
Sleep 200ms
Enter
Sleep 1.2s

Type "cc-clip setup myserver"
Sleep 200ms
Enter
Sleep 2.5s

Type "ssh myserver"
Sleep 200ms
Enter
Sleep 3s
