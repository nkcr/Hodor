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

The service has two components: a `server` and a `deployer`. The `server` is a
simple http server that listens on two endpoints:

```sh
// POST /api/hook/:releaseID
// GET /api/status/:jobID
```

The first endpoint triggers a new deployment and returns a `jobID`. It takes as
input the following:

```
application/json
{"browser_download_url": "<a valid URL>"}
```

and return the following:

```
application/json
{"jobID": "<Job id>"}
```

The second endpoint return the status of a job, given a `jobID`. It doesn't take
any input as the job is in the URL. It return the following:

```
application/json
{"status":"<status>","message":"<status message>"}
```