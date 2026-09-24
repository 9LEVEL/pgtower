# Records docs/demo.gif — regenerate with:  vhs docs/demo/demo.tape
#
# Needs two throwaway PostgreSQL clusters with demo data (ports 5434 "prod-db1"
# and 5435 "dev-local", password "demo") and the simulated traffic running:
#   docs/demo/setup.sh && DURATION=900 docs/demo/activity.sh
# Remove the clusters afterwards with: docs/demo/setup.sh --drop
# Port 5499 ("staging-eu") is intentionally closed, to show a connection error.
# Set PGTOWER_DEMO_BIN to a directory holding a release-tagged pgtower build so the
# header shows a version instead of "dev".

Output docs/demo.gif

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set Width 1440
Set Height 800
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 70ms
Set Framerate 24
Set PlaybackSpeed 1.0

Hide
Type "export PATH=${PGTOWER_DEMO_BIN:-$PATH}:$PATH PGTOWER_CONFIG=docs/demo/config.yml.demo PGTOWER_UPDATE_CHECK=0 PS1='$ ' && clear"
Enter
Show

Sleep 500ms
Type "pgtower"
Sleep 400ms
Enter
Sleep 4.5s

# Sessions: who is doing what, idle-in-transaction, waits
Type "5"
Sleep 3.5s

# Locks: the blocking tree
Type "4"
Sleep 3.5s

# Roles with attributes and connection limits
Type "6"
Sleep 3s

# Databases -> tables with sizes
Type "2"
Sleep 1.5s
Down
Sleep 400ms
Enter
Sleep 3s
Escape
Sleep 500ms

# Servers: test all three, then switch to dev
Type "S"
Sleep 1.2s
Type "t"
Sleep 700ms
Type "j"
Type "t"
Sleep 700ms
Type "j"
Type "t"
Sleep 3s
Type "k"
Sleep 500ms
Enter
Sleep 3.5s

# Tuning advisor on the dev server
Type "7"
Sleep 3.5s
