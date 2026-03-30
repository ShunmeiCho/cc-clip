# cc-clip Windows Demo

Output docs/marketing/demo-windows.gif
Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 900
Set Height 450
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set PlaybackSpeed 0.5

Hide
Type "export PS1='> '"
Enter
Type "SIM=docs/marketing/sim-windows.sh"
Enter
Type "alias cc-clip='bash $SIM hotkey #'"
Enter
Type "clear"
Enter
Sleep 500ms
Show

Type "cc-clip hotkey --enable-autostart"
Sleep 200ms
Enter
Sleep 4s
