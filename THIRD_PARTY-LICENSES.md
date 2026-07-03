# Third-Party Licenses

JackUI itself is released under the MIT License (see [`LICENSE`](./LICENSE)). It
is built on, and — when distributed as a **Docker image** — redistributes,
third-party components under their own licenses. This file summarizes them.

> This is a good-faith summary, not legal advice. The authoritative, complete
> dependency inventory is the **SBOM** generated on every release
> (CycloneDX via `cdxgen`, see `scripts/sbom-upload.sh`). If you redistribute
> the binary or image, review the licenses of the components below.

## Runtime image components (the parts that need attention)

The `Dockerfile.nvidia` image is based on `nvidia/cuda:12.2.0-runtime-ubuntu22.04`
and installs `ffmpeg` from Ubuntu's package repository. These are **not** MIT and
are the components most people care about when redistributing an image:

| Component | Source | License |
|---|---|---|
| **ffmpeg** | Ubuntu 22.04 `apt` package (built `--enable-nvenc --enable-cuvid`) | **GPL-2.0+ / GPL-3.0** (as packaged by Ubuntu, incl. GPL codecs like x264). Source: Ubuntu archive. |
| **NVIDIA CUDA runtime** | `nvidia/cuda` base image | **NVIDIA CUDA EULA** (redistribution governed by the EULA; not open source) |
| **Ubuntu 22.04 base** + `ca-certificates`, `tzdata` | Ubuntu archive | Mix of GPL / LGPL / MIT / BSD per package |

The CPU-only `Dockerfile` uses a distro base + `ffmpeg` as well; the ffmpeg/GPL
note applies there too. If you distribute these images, you must comply with the
GPL (offer corresponding source for ffmpeg — Ubuntu provides it) and the NVIDIA
CUDA EULA.

## Go modules (direct dependencies)

All permissive; MPL-2.0 is file-level copyleft (does not affect this project's code):

| Module | License |
|---|---|
| github.com/SherClockHolmes/webpush-go | MIT |
| github.com/anacrolix/log | MPL-2.0 |
| github.com/anacrolix/torrent | MPL-2.0 |
| github.com/dhowden/tag | BSD-2-Clause |
| github.com/gin-contrib/cors | MIT |
| github.com/gin-gonic/gin | MIT |
| github.com/go-webauthn/webauthn | BSD-3-Clause |
| github.com/golang-jwt/jwt/v5 | MIT |
| github.com/golang-migrate/migrate/v4 | MIT |
| github.com/jackc/pgx/v5 | MIT |
| github.com/nwaples/rardecode/v2 | BSD-2-Clause |
| github.com/prometheus/client_golang | Apache-2.0 |
| golang.org/x/crypto, x/sys, x/text, x/time | BSD-3-Clause |
| gopkg.in/yaml.v3 | MIT + Apache-2.0 |
| modernc.org/sqlite | BSD-3-Clause |

Transitive Go dependencies (the full module graph) are covered by the SBOM. Run
`go list -m all` for the complete list, or `go-licenses report ./...` to render
each module's license.

## npm packages (direct dependencies)

| Package | License |
|---|---|
| axios | MIT |
| hls.js | Apache-2.0 |
| i18next | MIT |
| lucide-react | ISC |
| react, react-dom | MIT |
| react-i18next | MIT |
| react-router-dom | MIT |

Full frontend tree: `cd web && npm ls --all` / `npx license-checker --summary`.
