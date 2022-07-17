<div align="center">
<img width="300" src=".assets/logo-white.png#gh-light-mode-only"/>
<img width="300" src=".assets/logo-white.png#gh-dark-mode-only"/>
</div>

# Hookable Deployment of Releases

[![Go Tests](https://github.com/nkcr/Hodor/actions/workflows/go.yml/badge.svg)](https://github.com/nkcr/Hodor/actions/workflows/go.yml)
[![Coverage Status](https://coveralls.io/repos/github/nkcr/Hodor/badge.svg?branch=main)](https://coveralls.io/github/nkcr/Hodor?branch=main)
[![Go Report Card](https://goreportcard.com/badge/github.com/nkcr/hodor)](https://goreportcard.com/report/github.com/nkcr/hodor)
[![Go Reference](https://pkg.go.dev/badge/github.com/nkcr/hodor.svg)](https://pkg.go.dev/github.com/nkcr/hodor)

Hodor is a service that deploys releases locally based on HTTP requests. It
can be used from a CI/CD to automatically deploy the new version of a software.

The service has two components: a `server` and a `deployer`. The `server` is an
http server that provides the following endpoints:

```sh
// POST /api/hook/:releaseID
// GET /api/status/:jobID
// GET /api/tags/:releaseID
```

The first endpoint triggers a new deployment and returns a `jobID`:

```sh
curl -X POST -d '{"browser_download_url": "<a valid URL>.tar.gz", "tag": "<optional tag>"}' /api/hook/o2vie
→ application/json
{"jobID": "<Job id>"}
```

The second endpoint return the status of a job, given a `jobID`. It doesn't take
any input as the job is in the URL:

```sh
curl -X GET /api/status/<jobID>   
→ application/json
{"status":"<status>","message":"<status message>"}
```

It is possible to get the latest deployed tag of a release, as a shields.io
badge, or in plain text:

```sh
# Get shields.io badge:
curl -X GET /api/tags/<releaseID>?format=SVG
→ text/html
<svg>...</svg>

# Get in plain text:
curl -X GET /api/tags/<releaseID>?format=SVG
→ text/plain
v1.0.0
```