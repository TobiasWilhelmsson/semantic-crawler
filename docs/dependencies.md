# Dependency licences

The [MIT licence](../LICENSE) covers this project's code. Dependencies retain
their own copyright and licence terms. This source release does not vendor
dependency code or distribute compiled binaries. Go downloads the versions
recorded in `go.mod` and verifies their checksums against `go.sum`.

The table records the 38 dependency modules imported by `go list -deps ./...`
on macOS for the first release. Additional platform-specific or optional modules
can appear in `go.mod`/`go.sum`; inspect their upstream notices when changing
the build or distributing binaries. Refresh this inventory when dependencies change.

| Module | Version | Licence |
| --- | --- | --- |
| [filippo.io/edwards25519](https://pkg.go.dev/filippo.io/edwards25519@v1.1.1?tab=licenses) | v1.1.1 | BSD-3-Clause |
| [github.com/JohannesKaufmann/dom](https://pkg.go.dev/github.com/JohannesKaufmann/dom@v0.2.0?tab=licenses) | v0.2.0 | MIT |
| [github.com/JohannesKaufmann/html-to-markdown/v2](https://pkg.go.dev/github.com/JohannesKaufmann/html-to-markdown/v2@v2.5.0?tab=licenses) | v2.5.0 | MIT |
| [github.com/PuerkitoBio/goquery](https://pkg.go.dev/github.com/PuerkitoBio/goquery@v1.12.0?tab=licenses) | v1.12.0 | BSD-3-Clause |
| [github.com/andybalholm/cascadia](https://pkg.go.dev/github.com/andybalholm/cascadia@v1.3.3?tab=licenses) | v1.3.3 | BSD-2-Clause |
| [github.com/aymerick/douceur](https://pkg.go.dev/github.com/aymerick/douceur@v0.2.0?tab=licenses) | v0.2.0 | MIT |
| [github.com/gabriel-vasile/mimetype](https://pkg.go.dev/github.com/gabriel-vasile/mimetype@v1.4.12?tab=licenses) | v1.4.12 | MIT |
| [github.com/gin-contrib/sse](https://pkg.go.dev/github.com/gin-contrib/sse@v1.1.0?tab=licenses) | v1.1.0 | MIT |
| [github.com/gin-gonic/gin](https://pkg.go.dev/github.com/gin-gonic/gin@v1.12.0?tab=licenses) | v1.12.0 | MIT |
| [github.com/go-playground/locales](https://pkg.go.dev/github.com/go-playground/locales@v0.14.1?tab=licenses) | v0.14.1 | MIT |
| [github.com/go-playground/universal-translator](https://pkg.go.dev/github.com/go-playground/universal-translator@v0.18.1?tab=licenses) | v0.18.1 | MIT |
| [github.com/go-playground/validator/v10](https://pkg.go.dev/github.com/go-playground/validator/v10@v10.30.1?tab=licenses) | v10.30.1 | MIT |
| [github.com/go-sql-driver/mysql](https://pkg.go.dev/github.com/go-sql-driver/mysql@v1.8.1?tab=licenses) | v1.8.1 | MPL-2.0 |
| [github.com/goccy/go-yaml](https://pkg.go.dev/github.com/goccy/go-yaml@v1.19.2?tab=licenses) | v1.19.2 | MIT |
| [github.com/google/uuid](https://pkg.go.dev/github.com/google/uuid@v1.6.0?tab=licenses) | v1.6.0 | BSD-3-Clause |
| [github.com/gorilla/css](https://pkg.go.dev/github.com/gorilla/css@v1.0.1?tab=licenses) | v1.0.1 | BSD-3-Clause |
| [github.com/jinzhu/inflection](https://pkg.go.dev/github.com/jinzhu/inflection@v1.0.0?tab=licenses) | v1.0.0 | MIT |
| [github.com/jinzhu/now](https://pkg.go.dev/github.com/jinzhu/now@v1.1.5?tab=licenses) | v1.1.5 | MIT |
| [github.com/joho/godotenv](https://pkg.go.dev/github.com/joho/godotenv@v1.5.1?tab=licenses) | v1.5.1 | MIT |
| [github.com/leodido/go-urn](https://pkg.go.dev/github.com/leodido/go-urn@v1.4.0?tab=licenses) | v1.4.0 | MIT |
| [github.com/mattn/go-isatty](https://pkg.go.dev/github.com/mattn/go-isatty@v0.0.20?tab=licenses) | v0.0.20 | MIT |
| [github.com/mattn/go-sqlite3](https://pkg.go.dev/github.com/mattn/go-sqlite3@v1.14.22?tab=licenses) | v1.14.22 | MIT |
| [github.com/microcosm-cc/bluemonday](https://pkg.go.dev/github.com/microcosm-cc/bluemonday@v1.0.27?tab=licenses) | v1.0.27 | BSD-3-Clause |
| [github.com/pelletier/go-toml/v2](https://pkg.go.dev/github.com/pelletier/go-toml/v2@v2.2.4?tab=licenses) | v2.2.4 | MIT |
| [github.com/quic-go/qpack](https://pkg.go.dev/github.com/quic-go/qpack@v0.6.0?tab=licenses) | v0.6.0 | MIT |
| [github.com/quic-go/quic-go](https://pkg.go.dev/github.com/quic-go/quic-go@v0.59.1?tab=licenses) | v0.59.1 | MIT |
| [github.com/ugorji/go/codec](https://pkg.go.dev/github.com/ugorji/go/codec@v1.3.1?tab=licenses) | v1.3.1 | MIT |
| [github.com/yuin/goldmark](https://pkg.go.dev/github.com/yuin/goldmark@v1.7.17?tab=licenses) | v1.7.17 | MIT |
| [go.mongodb.org/mongo-driver/v2](https://pkg.go.dev/go.mongodb.org/mongo-driver/v2@v2.5.0?tab=licenses) | v2.5.0 | Apache-2.0 |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto@v0.57.0?tab=licenses) | v0.57.0 | BSD-3-Clause |
| [golang.org/x/net](https://pkg.go.dev/golang.org/x/net@v0.60.0?tab=licenses) | v0.60.0 | BSD-3-Clause |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys@v0.48.0?tab=licenses) | v0.48.0 | BSD-3-Clause |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text@v0.42.0?tab=licenses) | v0.42.0 | BSD-3-Clause |
| [google.golang.org/protobuf](https://pkg.go.dev/google.golang.org/protobuf@v1.36.10?tab=licenses) | v1.36.10 | BSD-3-Clause |
| [gorm.io/datatypes](https://pkg.go.dev/gorm.io/datatypes@v1.2.7?tab=licenses) | v1.2.7 | MIT |
| [gorm.io/driver/mysql](https://pkg.go.dev/gorm.io/driver/mysql@v1.5.6?tab=licenses) | v1.5.6 | MIT |
| [gorm.io/driver/sqlite](https://pkg.go.dev/gorm.io/driver/sqlite@v1.6.0?tab=licenses) | v1.6.0 | MIT |
| [gorm.io/gorm](https://pkg.go.dev/gorm.io/gorm@v1.31.1?tab=licenses) | v1.31.1 | MIT |

The MySQL driver is an indirect dependency through GORM's JSON helpers; the
crawler uses SQLite and needs no MySQL service. MongoDB's BSON package is
pulled in by Gin's binding support; no MongoDB service is required. These
dependencies remain under MPL-2.0 and Apache-2.0 respectively. A future binary
distribution must include the notices and source availability required by its
dependencies; the project's MIT licence does not replace those terms.
