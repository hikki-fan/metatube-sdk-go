# MetaTube SDK Go

[![Build Status](https://img.shields.io/github/actions/workflow/status/metatube-community/metatube-sdk-go/docker.yml?branch=main&style=flat-square&logo=github-actions)](https://github.com/metatube-community/metatube-sdk-go/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/metatube-community/metatube-sdk-go?style=flat-square)](https://github.com/metatube-community/metatube-sdk-go)
[![Require Go Version](https://img.shields.io/badge/go-%3E%3D1.25-30dff3?style=flat-square&logo=go)](https://github.com/metatube-community/metatube-sdk-go/blob/main/go.mod)
[![GitHub License](https://img.shields.io/github/license/metatube-community/metatube-sdk-go?color=e4682a&logo=apache&style=flat-square)](https://github.com/metatube-community/metatube-sdk-go/blob/main/LICENSE)
[![Tag](https://img.shields.io/github/v/tag/metatube-community/metatube-sdk-go?color=%23ff8936&logo=fitbit&style=flat-square)](https://github.com/metatube-community/metatube-sdk-go/tags)

Metadata Tube SDK in Golang.

## Contents

- [MetaTube SDK Go](#metatube-sdk-go)
    - [Contents](#contents)
    - [Features](#features)
    - [Installation](#installation)
    - [Credits](#credits)
    - [License](#license)

## Features

- Supported platforms
    - Linux
    - Darwin
    - Windows
    - BSD(s)
- Supported Databases
    - [SQLite](https://gitlab.com/cznic/sqlite)
    - [PostgreSQL](https://github.com/jackc/pgx)
- Image processing
    - Auto cropping
    - Badge support
    - Face detection
    - Image hashing
- RESTful API
- 20+ providers
- Text translation

## Installation

To install this package, you first need [Go](https://golang.org/) installed (**go1.25+ is required**), then you can use
the below Go command to install SDK.

```sh
go get -u github.com/metatube-community/metatube-sdk-go
```

### JavDB CookieCloud session (optional)

JavDB can lazily synchronize browser cookies from a CookieCloud server before
searching or loading a detail page. Configure all three required variables on
the MetaTube server; the refresh interval defaults to ten minutes.

```sh
MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_HOST=https://cookiecloud.example.com
MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_UUID=<cookiecloud-uuid>
MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_PASSWORD=<cookiecloud-end-to-end-password>
# Optional:
MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_REFRESH_INTERVAL=10m
MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_USER_AGENT=<browser-user-agent>
# Optional rotating JavDB domain. The CookieCloud browser session must include this domain.
MT_MOVIE_PROVIDER_JAVDB__BASE_URL=https://javdb580.com/
```

Only cookies for `javdb.com`, an approved `javdb<digits>.com` mirror (and
their subdomains), or `jdbstatic.com` (and its subdomains) are imported.
Credentials stay in memory and are never logged or persisted by the SDK.

### AV-LEAGUE actor enrichment

AV-LEAGUE is the primary Japanese JAV actor provider. When an AV-LEAGUE
actor is fetched, MetaTube performs a strict, unique-name/alias match against
`JavDBActor` and appends only the JavDB name, aliases, and avatar. The
AV-LEAGUE provider ID, homepage, and structured profile fields remain the
source of truth. JavDB errors and ambiguous matches are ignored so the
primary metadata is not lost.

The actor provider can use the same optional CookieCloud session with the
following variables:

```sh
MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_HOST=https://cookiecloud.example.com
MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_UUID=<cookiecloud-uuid>
MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_PASSWORD=<cookiecloud-end-to-end-password>
# Optional:
MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_REFRESH_INTERVAL=10m
MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_USER_AGENT=<browser-user-agent>
MT_ACTOR_PROVIDER_JAVDBACTOR__BASE_URL=https://javdb580.com/
```

Cached actor records are enriched once per process within the retry window;
use a forced metadata refresh after enabling this feature to update existing
records immediately.

## Credits

| Library														                                           | Description																						                                                                    |
|-----------------------------------------------------------------|------------------------------------------------------------------------------------------------------|
| [gocolly/colly](https://github.com/gocolly/colly)			            | Elegant Scraper and Crawler Framework for Golang													                                        |
| [gin-gonic/gin](https://github.com/gin-gonic/gin)			            | Gin is a HTTP web framework written in Go															                                             |
| [gorm.io/gorm](https://gorm.io/)								                        | The fantastic ORM library for Golang																                                                 |
| [esimov/pigo](https://github.com/esimov/pigo)				               | Fast face detection, pupil/eyes localization and facial landmark points detection library in pure Go |
| [robertkrimen/otto](https://github.com/robertkrimen/otto)       | A JavaScript interpreter in Go (golang)                                                              |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite)		         | Package sqlite is a CGo-free port of SQLite/SQLite3												                                      |
| [corona10/goimagehash](https://github.com/corona10/goimagehash) | Go Perceptual image hashing package																                                                  |
| [antchfx/xpath](https://github.com/antchfx/xpath)			            | XPath package for Golang, supports HTML, XML, JSON document query									                           |
| [gen2brain/jpegli](https://github.com/gen2brain/jpegli)         | Go encoder/decoder for JPEG based on jpegli                                                          |

## License

[Apache-2.0 License](https://github.com/metatube-community/metatube-sdk-go/blob/main/LICENSE)
