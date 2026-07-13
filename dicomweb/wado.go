package dicomweb

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// RetrieveInstance fetches one SOP Instance as Part 10 bytes (WADO-RS).
func (c *Client) RetrieveInstance(ctx context.Context, studyUID, seriesUID, instanceUID string) ([]byte, error) {
	if studyUID == "" || seriesUID == "" || instanceUID == "" {
		return nil, fmt.Errorf("dicomweb: study/series/instance UID required")
	}
	url, err := c.resolve("studies", studyUID, "series", seriesUID, "instances", instanceUID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", fmt.Sprintf(`%s; type="%s", %s`, MediaTypeMultipart, MediaTypeDICOM, MediaTypeDICOM))

	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	parts, err := readDICOMParts(resp.Body, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	if len(parts) != 1 {
		return nil, fmt.Errorf("dicomweb: expected 1 instance part, got %d", len(parts))
	}
	return parts[0], nil
}

// RetrieveInstanceFile is RetrieveInstance plus godicom.ReadBytes.
func (c *Client) RetrieveInstanceFile(ctx context.Context, studyUID, seriesUID, instanceUID string) (*godicom.FileDataset, error) {
	raw, err := c.RetrieveInstance(ctx, studyUID, seriesUID, instanceUID)
	if err != nil {
		return nil, err
	}
	return godicom.ReadBytes(raw, nil)
}

// RetrieveInstanceMetadata fetches Instance metadata as DICOM JSON (WADO-RS).
func (c *Client) RetrieveInstanceMetadata(ctx context.Context, studyUID, seriesUID, instanceUID string) (*godicom.Dataset, error) {
	if studyUID == "" || seriesUID == "" || instanceUID == "" {
		return nil, fmt.Errorf("dicomweb: study/series/instance UID required")
	}
	url, err := c.resolve("studies", studyUID, "series", seriesUID, "instances", instanceUID, "metadata")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", MediaTypeDICOMJSON)

	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	datasets, err := dicomjson.ParseDatasets(body)
	if err != nil {
		return nil, err
	}
	if len(datasets) == 0 {
		return nil, fmt.Errorf("dicomweb: empty metadata")
	}
	return datasets[0], nil
}
