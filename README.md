# hyped/defmt_decoder

Go bindings for [defmt-decoder](https://crates.io/crates/defmt-decoder) through a Rust C FFI.

```go
decoders, err := defmt_decoder.Init([]string{"firmware.elf"})
if err != nil {
	log.Fatal(err) // with ErrorInAtLeastOneEntry, the paths that loaded still have a decoder
}

dec := decoders[0]
go func() {
	io.Copy(dec, rttSource) // raw bytes from the target
	dec.Close()
}()

scanner := bufio.NewScanner(dec) // one line per frame, plus a "└─ module @ file:line" line if the ELF has debug info
for scanner.Scan() {
	fmt.Println(scanner.Text())
}
```

`Init` can only be called once per process. Each `Decoder` is an `io.ReadWriteCloser`: raw
bytes go in through `Write`, and decoded text comes out of `Read`, which blocks until a frame is
available or the decoder is closed. Malformed frames are logged and skipped.

# Memory Safety
The rust wrapper focuses on the design, maintainability, and memory safety rather than performance. As a result, every stream is behind a `std::sync::Mutex`, and nothing is leaked onto the heap to have no unsafe {} usage outside of the FFI layer

# Contribute

## Layout

| Path | What it is |
|---|---|
| `defmt_decoder.go` | the Go API, calls into the Rust library through cgo |
| `rust_ffi/` | the Rust C FFI wrapper around `defmt-decoder`, `cbindgen` generates its header |
| `build/` | **committed** static libraries and header, one folder per platform. This is what users of the module get. Only CI updates it |
| `build-dev/` | your local builds, git-ignored |
| `cgo_release.go` | links `build/`. The default, so it is what users compile |
| `cgo_dev.go` | links `build-dev/`, only with `-tags defmt_decoder_dev` |
| `.github/consumer/` | a separate module that uses this one the way a user would, for CI |

## Requirements

Go, Rust (stable, >= 1.85), `make` and a C compiler for cgo. On Windows, use MSYS2's MinGW-w64
gcc and `make`, and set `CARGO_BUILD_TARGET=x86_64-pc-windows-gnu`, since cgo links with MinGW.

## Building and testing

| Command | What it does |
|---|---|
| `make test_dev` (or `make test`) | builds into `build-dev/` and runs the tests against it |
| `make test_dev_stripped` | the same with the library stripped, as releases are |
| `make build_dev` | only builds into `build-dev/` |
| `make test_release` | tests the committed `build/` libraries without building anything |
| `make build_release` | builds and strips into `build/`. This is what CI runs |

The `*_release` targets print a warning because they use or change the committed `build/`.
Don't commit a local `build/`: undo it with `git restore build/`.

The dev tag is named after the package because build tags apply to every package in a build: a
user's own `dev` tag must not switch this package to `build-dev/`, which isn't in the module.
To have gopls check `cgo_dev.go`, add `-tags=defmt_decoder_dev` to its `buildFlags`.

## CI and releases

- `build_and_test.yml` builds, strips and tests on Linux (amd64, arm64), macOS (arm64) and
  Windows (amd64), for every PR and every push to `main`.
- On every push to `main`, `publish.yml` updates the release PR opened by
  [release-please](https://github.com/googleapis/release-please). It then commits the freshly
  built `build/` onto that PR, and `consumer_test.yml` fetches that exact commit as a dependency
  on all four platforms.
- Merging the release PR makes release-please tag the merge commit, which is the release. The
  tag is checked once more through the Go module proxy. Wait for the latest Publish run to pass
  before merging.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/), which
release-please uses to pick the next version and write the changelog: `fix:` releases a patch,
`feat:` a minor version, and `chore:`, `docs:`, `ci:` and the like don't release anything. With
squash merges, the PR title is the commit message that counts.
