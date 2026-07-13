# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Phase 3 DICOMweb MVP: `dicomweb` client (WADO-RS instance/metadata, STOW-RS, QIDO-RS studies) + `Handler`/`MemoryStore` origin-server
- Depend on godicom `v0.23.0` (`EncodeFile` / `ReadBytes` / `dicomjson.ParseDatasets`)
- Phase 2 C-MOVE / C-GET: `dimse` command sets + sub-operation counts, `ae.CMove` / `ae.CGet` SCU (C-GET demuxes interleaved C-STORE), `ae.Serve` `OnCMove` / `OnCGet` SCP
- Phase 2 C-FIND: `dimse` C-FIND-RQ/RSP, `ae.CFind` SCU, `ae.Serve` `OnCFind` SCP (Patient/Study root models)
- Depend on godicom `v0.22.1` (`DecodeDataset` + encode race fix)

### Added (prior)
- Depend on godicom `v0.21.0`; `StoreRequest.Data` encodes via `Dataset.Encode` under the negotiated transfer syntax

### Added (Phase 2 C-STORE)
- Phase 2 C-STORE: `dimse` C-STORE-RQ/RSP, PDV fragmentation, `ae.CStore` SCU, `ae.Serve` SCP with `OnCStore`
- Multi presentation-context association negotiation on SCU `Config`
- Golden fixture roundtrips for C-STORE command sets (pynetdicom test bytes)

### Added (Phase 1)
- Phase 1 DIMSE foundation: `pdu` (A-ASSOCIATE / P-DATA-TF / A-RELEASE / A-ABORT), `dimse` C-ECHO command sets, `ae` Association SCU + `CEcho`
- Golden fixture roundtrips from pynetdicom test bytes; mock SCP C-ECHO integration tests
- GitHub Actions CI (`go test -race`, `go vet`)

### Added (bootstrap)
- Repository bootstrap: Go module (`godicom` v0.20.0), `pynetdicom` submodule, package docs + smoke test
