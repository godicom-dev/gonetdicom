# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- CI: `golangci-lint` job + `.golangci.yml`

## [0.1.0] - 2026-07-13

First tagged release — Phase 1–3 foundation.

### Added
- Phase 1 DIMSE: `pdu`, `dimse` C-ECHO, `ae` Association SCU + `CEcho`
- Phase 2 DIMSE: C-STORE / C-FIND / C-MOVE / C-GET SCU/SCP (godicom encode/decode)
- Phase 3 DICOMweb: `dicomweb` WADO-RS (study/series/instance + metadata), STOW-RS, QIDO-RS (studies/series/instances); `Handler` + `MemoryStore`
- Depend on godicom `v0.23.0`
- GitHub Actions CI (`go test -race`, `go vet`, `golangci-lint`)
