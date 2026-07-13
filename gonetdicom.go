// Package gonetdicom provides DICOM networking for Go.
//
// It depends on [github.com/godicom-dev/godicom] for Dataset / file I/O / pixels,
// and uses [pynetdicom](https://github.com/pydicom/pynetdicom) (git submodule) as
// the primary behavioural reference for DIMSE. DICOMweb (WADO-RS / QIDO-RS /
// STOW-RS) is also in scope; that path follows DICOM PS3.18 rather than a
// pynetdicom 1:1 port.
package gonetdicom
