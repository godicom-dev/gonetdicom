package dicomweb

import (
	"fmt"
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// pixelDataTag is (7FE0,0010).
var pixelDataTag = godicom.MustTag("PixelData")

// BulkDataURI builds the WADO-RS Pixel Data bulkdata path for an instance
// (relative to the Studies Service root, PS3.18-style).
func BulkDataURI(prefix, studyUID, seriesUID, instanceUID string) string {
	prefix = strings.TrimRight(prefix, "/")
	return fmt.Sprintf("%s/studies/%s/series/%s/instances/%s/bulkdata",
		prefix, studyUID, seriesUID, instanceUID)
}

// prepareMetadataForJSON replaces Pixel Data with a 1-byte stub so DICOM JSON
// marshaling can emit BulkDataURI without base64-encoding multi-MB pixels.
func prepareMetadataForJSON(ds *godicom.Dataset) *godicom.Dataset {
	if ds == nil {
		return nil
	}
	out := ds.Clone()
	if _, ok := out.Get(pixelDataTag); ok {
		out.Set(godicom.NewDataElement(pixelDataTag, godicom.VROB, []byte{0}))
	}
	return out
}

func marshalInstanceMetadata(ds *godicom.Dataset, bulkURI string) ([]byte, error) {
	prepared := prepareMetadataForJSON(ds)
	return dicomjson.MarshalDatasets([]*godicom.Dataset{prepared},
		dicomjson.WithBulkDataThreshold(0),
		dicomjson.WithBulkDataURIBuilder(func(tag godicom.Tag, _ godicom.VR, _ []byte) (string, error) {
			if tag != pixelDataTag {
				return "", fmt.Errorf("dicomweb: no BulkDataURI for %s", tag)
			}
			return bulkURI, nil
		}),
	)
}

func marshalManyMetadata(metas []*godicom.Dataset, prefix, study, series string) ([]byte, error) {
	prepared := make([]*godicom.Dataset, len(metas))
	for i, ds := range metas {
		prepared[i] = prepareMetadataForJSON(ds)
	}
	idx := 0
	return dicomjson.MarshalDatasets(prepared,
		dicomjson.WithBulkDataThreshold(0),
		dicomjson.WithBulkDataURIBuilder(func(tag godicom.Tag, _ godicom.VR, _ []byte) (string, error) {
			if tag != pixelDataTag {
				return "", fmt.Errorf("dicomweb: no BulkDataURI for %s", tag)
			}
			if idx >= len(metas) {
				return "", fmt.Errorf("dicomweb: BulkDataURI index overflow")
			}
			ds := metas[idx]
			idx++
			inst, _ := ds.GetString(godicom.MustTag("SOPInstanceUID"))
			ser := series
			if ser == "" {
				ser, _ = ds.GetString(godicom.MustTag("SeriesInstanceUID"))
			}
			stu := study
			if stu == "" {
				stu, _ = ds.GetString(godicom.MustTag("StudyInstanceUID"))
			}
			return BulkDataURI(prefix, stu, ser, inst), nil
		}),
	)
}
