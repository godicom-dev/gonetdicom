# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- User Identity Negotiation (PDU `0x58`/`0x59`): `pdu.UserIdentityRQ`/`UserIdentityAC`, `ae.Config.UserIdentity`, `ServerConfig.OnUserIdentity`, helpers `UsernameIdentity` / `UsernamePasscodeIdentity` (pynetdicom-aligned accept/reject + Kerberos/SAML/JWT AC response)

## [0.5.0] - 2026-07-13

### Added
- C-MOVE SCP performs real C-STORE sub-operations to Move Destination: `MovePlan`, `ServerConfig.MoveDestinations`, Move Originator AE/Message ID on outbound stores

### Changed
- **Breaking:** `OnCMove` now returns `MovePlan` (`Stores` + `Responses`) instead of `[]RetrieveMatch`

## [0.4.0] - 2026-07-13

### Added
- SCP/SCU Role Selection Negotiation (PDU `0x54`): `pdu.RoleSelection`, `ae.BuildRole`, `Config`/`ServerConfig.RoleSelections`, negotiated `AcceptedContext.AsSCU`/`AsSCP` (pynetdicom-aligned)
- C-CANCEL-RQ (`dimse.CCancelRQ`, `ae.Association.CCancel`); SCP peeks for cancel between C-FIND/C-MOVE/C-GET pending responses (status `0xFE00`)
- DIMSE-N Storage Commitment Push Model MVP: `N-ACTION` / `N-EVENT-REPORT` encode/decode + `ae.NAction` / `ae.NEventReport` with same-association event push (`OnNAction` / `OnNEventReport`)

## [0.3.0] - 2026-07-13

### Added
- WADO-RS Retrieve Rendered (instance-level JPEG/PNG via godicom pixel pipeline)
- WADO-RS Pixel Data bulkdata (`application/octet-stream`) client + origin-server routes
- PS3.18 HTTP error-path tests for rendered/bulkdata (404/406, missing PixelData, empty UIDs)

## [0.2.0] - 2026-07-13

### Added
- Phase 4 harden: DIMSE TLS (`Config.TLS` / `ListenAndServeTLS`), `IdleTimeout`, optional `slog` on AE + DICOMweb
- `dicomweb.NewClient` options (`WithTLSConfig`, `WithTimeout`, `WithLogger`, `WithHTTPClient`)
- Optional real-PACS soak: `go test -tags=integration ./ae -run TestIntegrationCEchoPACS`

## [0.1.0] - 2026-07-13

First tagged release — Phase 1–3 foundation.

### Added
- Phase 1 DIMSE: `pdu`, `dimse` C-ECHO, `ae` Association SCU + `CEcho`
- Phase 2 DIMSE: C-STORE / C-FIND / C-MOVE / C-GET SCU/SCP (godicom encode/decode)
- Phase 3 DICOMweb: `dicomweb` WADO-RS (study/series/instance + metadata), STOW-RS, QIDO-RS (studies/series/instances); `Handler` + `MemoryStore`
- Depend on godicom `v0.23.0`
- GitHub Actions CI (`go test -race`, `go vet`, `golangci-lint`)
