# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Phase 1 DIMSE foundation: `pdu` (A-ASSOCIATE / P-DATA-TF / A-RELEASE / A-ABORT), `dimse` C-ECHO command sets, `ae` Association SCU + `CEcho`
- Golden fixture roundtrips from pynetdicom test bytes; mock SCP C-ECHO integration tests
- GitHub Actions CI (`go test -race`, `go vet`)

### Added (bootstrap)
- Repository bootstrap: Go module (`godicom` v0.20.0), `pynetdicom` submodule, package docs + smoke test
