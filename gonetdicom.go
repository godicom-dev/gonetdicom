// Package gonetdicom provides DICOM networking for Go.
//
// It depends on [github.com/godicom-dev/godicom] for Dataset / file I/O / pixels,
// and uses [pynetdicom](https://github.com/pydicom/pynetdicom) (git submodule) as
// the primary behavioural reference for DIMSE. DICOMweb (WADO-RS / QIDO-RS /
// STOW-RS) follows DICOM PS3.18.
//
// Subpackages:
//   - [github.com/godicom-dev/gonetdicom/pdu] — Upper Layer PDUs
//   - [github.com/godicom-dev/gonetdicom/dimse] — DIMSE command sets
//   - [github.com/godicom-dev/gonetdicom/ae] — Association SCU/SCP
//   - [github.com/godicom-dev/gonetdicom/dicomweb] — DICOMweb client + origin-server MVP
package gonetdicom
