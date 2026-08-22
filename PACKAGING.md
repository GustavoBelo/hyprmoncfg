# Packaging hyprmoncfg

This repository is the upstream source of truth for release artifacts and shared
packaging assets. Distro-specific package recipes should usually live in the
package repository for that distro, not in this repository.

## Upstream Release Assets

Each tagged release publishes:

- `hyprmoncfg_<version>_linux_amd64.tar.gz`
- `hyprmoncfg_<version>_linux_arm64.tar.gz`
- `hyprmoncfg-<version>-deps.tar.xz`
- `checksums.txt`
- GitHub's automatic source archive for the tag

The binary archives contain:

- `hyprmoncfg`
- `hyprmoncfgd`
- `README.md`
- `LICENSE`
- `packaging/applications/hyprmoncfg.desktop`
- `packaging/applications/hyprmoncfg-omarchy.desktop`
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
version=1.15.0
commit="$(git rev-parse --short HEAD)"
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
tar -xf hyprmoncfg-1.15.0-deps.tar.xz
GOMODCACHE="$PWD/go-mod" GOPROXY=off CGO_ENABLED=0 go build -trimpath -mod=readonly ./cmd/hyprmoncfg
```

## Installed Files

Recommended installed files:

```text
/usr/bin/hyprmoncfg
/usr/bin/hyprmoncfgd
/usr/share/applications/hyprmoncfg.desktop
/usr/share/applications/hyprmoncfg-omarchy.desktop
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

Current status as of 2026-08-22:

| Channel | Status | Notes |
|---|---|---|
| Arch AUR | Published | Stable [`hyprmoncfg`](https://aur.archlinux.org/packages/hyprmoncfg) is published at 1.15.0. VCS [`hyprmoncfg-git`](https://aur.archlinux.org/packages/hyprmoncfg-git) continues to track `main`. |
| Fedora COPR | Published | [`paolino/hyprmoncfg`](https://copr.fedorainfracloud.org/coprs/paolino/hyprmoncfg/) build [10892566](https://copr.fedorainfracloud.org/coprs/build/10892566) publishes 1.15.0 for Fedora 44, 45, and rawhide on `x86_64` and `aarch64`. |
| Nixpkgs | Open PR | The source lives at `pkgs/by-name/hy/hyprmoncfg`; the package is available as `pkgs.hyprmoncfg` and `nixpkgs#hyprmoncfg`. The all-green [1.15.0 update](https://github.com/NixOS/nixpkgs/pull/552223) is under review. |
| Gentoo GURU | Published | [`gui-apps/hyprmoncfg`](https://github.com/gentoo/guru/tree/dev/gui-apps/hyprmoncfg) is published at 1.15.0 on the GURU `dev` branch. The update and its preceding version bumps have valid OpenPGP signatures and DCO trailers. |
| Void Linux official | Blocked | A local `hyprmoncfg` template exists, but official submission is not useful while Hyprland is not in Void. Multiple Hyprland package requests and PRs have been closed upstream, and the current Void maintainer stance is that Hyprland is not planned. |
| Void Blackhole-vl | Unofficial | [Blackhole-vl](https://github.com/Event-Horizon-VL/blackhole-vl) publishes `hyprland` and `hyprmoncfg` packages outside official Void. Its independently maintained [1.12.0 update](https://github.com/Event-Horizon-VL/blackhole-vl/pull/261) is merged; later versions follow that maintainer's own cadence. |
| Alpine aports | Open MR | [`alpine/aports!103051`](https://gitlab.alpinelinux.org/alpine/aports/-/merge_requests/103051) targets 1.15.0 and its pipeline passes; no package is in the Alpine package index yet. |
| Debian and Ubuntu | Salsa published | The 1.15.0 Debian source package builds successfully, and the [`debian/sid` branch and upstream tags are published on Salsa](https://salsa.debian.org/crmne/hyprmoncfg). Official inclusion still needs Debian policy review and the sponsor/upload flow. |
| openSUSE OBS | Publish pending | [`home:paolino/hyprmoncfg`](https://build.opensuse.org/package/show/home:paolino/hyprmoncfg) still builds 1.14.2 for openSUSE Tumbleweed on `x86_64`; the tested 1.15.0 payload is staged locally and awaits `osc` authentication. |
| SlackBuilds.org | Staged | The 1.15.0 SlackBuild payload is ready for manual submission. |

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
test -f /usr/share/applications/hyprmoncfg-omarchy.desktop
test -f /usr/share/icons/hicolor/scalable/apps/hyprmoncfg.svg
```

In a real Hyprland session, also verify:

```sh
hyprmoncfg list
systemctl --user daemon-reload
systemctl --user status hyprmoncfgd
```
