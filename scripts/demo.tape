Output docs/assets/images/demo.gif
Output docs/assets/images/demo.mp4

Set Shell "fish"
Set FontSize 14
Set FontFamily "BlexMono Nerd Font"
Set Width 1400
Set Height 800
Set Padding 12
Set Theme "tokyonight"

Hide
Type "set -e NO_COLOR; set -e FORCE_COLOR; set -e CLICOLOR_FORCE; set -gx TERM xterm-256color; set -gx COLORTERM truecolor; set -gx CLICOLOR 1; clear"
Enter
Sleep 300ms
Show

Type "hyprmoncfg"
Enter
Sleep 2s

# Arrange monitors directly on the canvas
Right 3
Sleep 1s
Left 3
Sleep 1s

# Select the other monitor
Type "]"
Sleep 1s

# Focus Display settings and browse its fields
Tab
Sleep 1s
Down 2
Sleep 1s

# Switch the inspector to Color
Type "]"
Sleep 1s
Down 2
Sleep 1s

# Return to Display, then the canvas
Type "["
Sleep 1s
Tab
Sleep 1s

# Restore the live layout before leaving the editor
Type "r"
Sleep 1.5s

# Browse saved hardware-aware profiles
Type "2"
Sleep 1s
Down 2
Sleep 1s
Up
Sleep 1s

# Preview workspace planning
Type "3"
Sleep 1s
Down
Sleep 1s
Right
Sleep 1s
Left
Sleep 1s

# Reset the draft and finish on the spatial editor
Type "r"
Sleep 1s
Type "1"
Sleep 2s

# Done
Sleep 1s
