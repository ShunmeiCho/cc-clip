# cc-clip Quick Demo

Output docs/marketing/demo-quick.gif
Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 18
Set Width 720
Set Height 320
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 30ms
Set PlaybackSpeed 0.5

Hide
Type "export PS1='$ '"
Enter
Type "SIM=docs/marketing/sim-quick.sh"
Enter
Type "alias curl='bash $SIM install #'"
Enter
Type "alias cc-clip='bash $SIM setup #'"
Enter
Type "clear"
Enter
Sleep 500ms
Show

Type "curl -fsSL https://raw.githubusercontent.com/ShunmeiCho/cc-clip/main/scripts/install.sh | sh"
Sleep 200ms
Enter
Sleep 1s

Type "cc-clip setup myserver"
Sleep 200ms
Enter
Sleep 4s
