# Contributing to clipx

Thanks for your interest in contributing! Here's how to get started.

## Development

### Prerequisites

- Go 1.25+ (or use [mise](https://mise.jdx.dev/) — the repo includes a `.mise/config.toml`)
- macOS (clipx uses `pbcopy`/`pbpaste`)

### Build & test

```bash
make build      # build binary to ./clipx-bin
make test       # run tests
make test-race  # run tests with race detector
make check      # fmt + vet + test
make coverage   # generate HTML coverage report
```

### Project structure

```
cmd/clipx/         CLI entry point (commands, LaunchAgent management)
clipx/
  clipboard.go     Clipboard abstraction (pbcopy/pbpaste)
  config.go        Config file (~/.config/clipx/config.json)
  net.go           Network utilities (DNS resolution, ping)
  node.go          Core daemon (listener, clipboard watcher, peer sync)
  protocol.go      Wire protocol (encode/decode messages & chunks)
  *_test.go        Tests
```

### Running locally

```bash
# terminal 1 — start a node
make build && ./clipx-bin

# terminal 2 — pair and test
./clipx-bin pair 127.0.0.1
```

## Submitting changes

1. Fork the repo
2. Create a feature branch (`git checkout -b my-feature`)
3. Make your changes
4. Run `make check` to ensure everything passes
5. Commit with a [conventional commit](https://www.conventionalcommits.org/) message
6. Open a pull request

## Commit messages

We use [conventional commits](https://www.conventionalcommits.org/):

- `feat:` new features
- `fix:` bug fixes
- `perf:` performance improvements
- `docs:` documentation changes
- `test:` test changes
- `chore:` maintenance tasks

## Reporting issues

Please open a [GitHub issue](https://github.com/gomantics/clipx/issues) with:

- Your macOS version
- `clipx version` output
- Steps to reproduce
- Relevant logs from `~/Library/Logs/clipx.log`

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
