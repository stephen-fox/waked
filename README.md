# waked

waked executes programs when macOS resumes from sleep. By default,
it executes all programs found in directory-path. If directory-path
is not specified, then `/usr/local/etc/waked` is used.

Executables containing '-on-unlock' in their name will only be executed
once the screen is unlocked.

waked will continuously re-execute a program if it exits with a non-zero
exit status.

## Example

```console
$ # Using the default directory:
$ waked
$ # Alternatively, specify a custom directory:
$ waked ~/.waked/
```

## Installation

1. `xcode-select --install` (I know, unfortunate)
2. `go install gitlab.com/stephen-fox/waked@latest`
3. `sudo cp ~/go/bin/waked /usr/local/bin/`

## Daemon setup

1. `cp /path/to/repo/Library/LaunchAgents/com.gitlab.stephen-fox.waked.plist ~/Library/LaunchAgents/`
2. `launchctl load ~/Library/LaunchAgents/com.gitlab.stephen-fox.waked.plist`

## Stop daemon

`launchctl unload -w ~/Library/LaunchAgents/com.gitlab.stephen-fox.waked.plist`

## Custom screen unlock check logic

If you would like to implement your own screen unlock checking logic in
a shell script, you can use this shell function:

```sh
# screen_is_locked is based on work by Joel Bruner:
# https://stackoverflow.com/a/66723000
#
# For an Objective-C implementation, see:
# https://stackoverflow.com/a/76241560
#
# This version avoids creating temporary files (via '<<<') by using plutil.
#
# Returns 0 if screen is locked.
screen_is_locked() {
  [ "$(/usr/sbin/ioreg -n Root -d1 -a \
    | /usr/bin/plutil \
      -extract 'IOConsoleUsers.0.CGSSessionScreenIsLocked' \
      raw -)" \
    == 'true' ]

  return $?
}
```
