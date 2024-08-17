# waked

waked executes programs when macOS resumes from sleep. By default,
it executes all programs found in directory-path. If directory-path
is not specified, then '/usr/local/waked' is used.

## Example

```console
$ waked ~/.waked/
```

## Installation

1. `xcode-select --install`
2. `go install gitlab.com/stephen-fox/waked@latest`
3. `sudo cp ~/go/bin/waked /usr/local/bin/`

## Daemon setup

1. `cp /path/to/repo/Library/LaunchAgents/com.gitlab.stephen-fox.waked.plist ~/Library/LaunchAgents/`
2. `launchctl load ~/Library/LaunchAgents/com.gitlab.stephen-fox.waked.plist`

## Stop daemon

`launchctl unload -w ~/Library/LaunchAgents/com.gitlab.stephen-fox.waked.plist`
