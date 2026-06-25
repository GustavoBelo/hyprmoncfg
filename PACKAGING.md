# Packaging hyprmoncfg

This repository is the upstream source of truth for release artifacts and shared
packaging assets. Distro-specific package recipes should usually live in the
package repository for that distro, not in this repository.

## Upstream Release Assets

Each tagged release publishes:

- `hyprmoncfg_<version>_linux_amd64.tar.gz`
- `hyprmoncfg_<version>_linux_arm64.tar.gz`
- `checksums.txt`
- GitHub's automatic source archive for the tag

The binary archives contain:

- `hyprmoncfg`
- `hyprmoncfgd`
- `README.md`
- `LICENSE`
- `packaging/applications/hyprmoncfg.desktop`
- `packaging/icons/hyprmoncfg.svg`
- `packaging/systemd/hyprmoncfgd.service`
- `packaging/systemd/hyprmoncfgd.local.service`

Source-based packages should avoid fetching Go modules during the package build.
Use a pre-fetched Go module cache tarball or the distro's native Go dependency
mechanism.

## Dependencies

Runtime:

- `hyprland`, specifically `hyprctl` in `PATH`
- `systemd` only for the packaged user service
- UPower is optional; it improves immediate lid-change detection

Build time:

- Go `1.26.1` or newer, matching `go.mod`

## Build From Source

Packagers should set build metadata through `internal/buildinfo`:

```sh
version=1.5.1
commit=509f68e
build_date="$(date -u +%FT%TZ)"
ldflags="-s -w"
ldflags="$ldflags -X github.com/crmne/hyprmoncfg/internal/buildinfo.Version=$version"
ldflags="$ldflags -X github.com/crmne/hyprmoncfg/internal/buildinfo.Commit=$commit"
ldflags="$ldflags -X github.com/crmne/hyprmoncfg/internal/buildinfo.Date=$build_date"

CGO_ENABLED=0 go build -trimpath -mod=readonly -ldflags "$ldflags" -o hyprmoncfg ./cmd/hyprmoncfg
CGO_ENABLED=0 go build -trimpath -mod=readonly -ldflags "$ldflags" -o hyprmoncfgd ./cmd/hyprmoncfgd
go test ./...
```

For offline builds with a Go module cache tarball:

```sh
tar -xf hyprmoncfg-1.5.1-deps.tar.xz
GOMODCACHE="$PWD/go-mod" GOPROXY=off CGO_ENABLED=0 go build -trimpath -mod=readonly ./cmd/hyprmoncfg
```

## Installed Files

Recommended installed files:

```text
/usr/bin/hyprmoncfg
/usr/bin/hyprmoncfgd
/usr/share/applications/hyprmoncfg.desktop
/usr/share/icons/hicolor/scalable/apps/hyprmoncfg.svg
/usr/share/licenses/hyprmoncfg/LICENSE
/usr/share/doc/hyprmoncfg/README.md
```

For systemd-based distros, also install:

```text
/usr/lib/systemd/user/hyprmoncfgd.service
```

Do not enable or start the user service from package scripts. Users should opt in
with:

```sh
systemctl --user enable --now hyprmoncfgd
```

For non-systemd distros, document `exec-once = hyprmoncfgd` in Hyprland config as
the daemon startup path.

## Package Status

Current status as of 2026-06-25:

| Channel | Status | Notes |
|---|---|---|
| Arch AUR | Published | Stable [`hyprmoncfg`](https://aur.archlinux.org/packages/hyprmoncfg) and VCS [`hyprmoncfg-git`](https://aur.archlinux.org/packages/hyprmoncfg-git) packages are maintained in separate AUR repos. |
| Fedora COPR | Published | [`paolino/hyprmoncfg`](https://copr.fedorainfracloud.org/coprs/paolino/hyprmoncfg/) has successful 1.8.0 builds for Fedora 44 and rawhide on `x86_64` and `aarch64`. |
| Nixpkgs | Published | The source lives at `pkgs/by-name/hy/hyprmoncfg`; the package is available as `pkgs.hyprmoncfg` and `nixpkgs#hyprmoncfg`. |
| Gentoo GURU | Published | `gui-apps/hyprmoncfg` is in [Gentoo GURU](https://github.com/gentoo/guru/tree/master/gui-apps/hyprmoncfg). |
| Void Linux official | Blocked | A local `hyprmoncfg` template exists, but official submission is not useful while Hyprland is not in Void. Multiple Hyprland package requests and PRs have been closed upstream, and the current Void maintainer stance is that Hyprland is not planned. |
| Void Blackhole-vl | Unofficial | [Blackhole-vl](https://github.com/Event-Horizon-VL/blackhole-vl) publishes prebuilt `hyprland` and `hyprmoncfg` packages outside official Void. As of 2026-06-25 its mirror has `hyprland-0.55.4_1` and `hyprmoncfg-1.7.0_1` for `x86_64`, `aarch64`, and musl variants. |
| Alpine aports | Open MR | [`alpine/aports!103051`](https://gitlab.alpinelinux.org/alpine/aports/-/merge_requests/103051) is open with a successful pipeline; no package is in the Alpine package index yet. |
| Debian and Ubuntu | Staged | Source package artifacts are staged externally, but official inclusion still needs Debian policy review and sponsor/upload flow. |
| openSUSE OBS | Staged | RPM payload is ready for OBS, but not published. |
| SlackBuilds.org | Staged | SlackBuild payload is ready for manual submission, but not published. |

Distro-specific recipes should remain in the distro package repository or the
external packaging workspace until they are accepted upstream. Keep this
repository limited to release assets and shared packaging files.

## Smoke Tests

After packaging, run:

```sh
hyprmoncfg version
hyprmoncfg --help
hyprmoncfgd --help
test -f /usr/share/applications/hyprmoncfg.desktop
test -f /usr/share/icons/hicolor/scalable/apps/hyprmoncfg.svg
```

In a real Hyprland session, also verify:

```sh
hyprmoncfg list
systemctl --user daemon-reload
systemctl --user status hyprmoncfgd
```
