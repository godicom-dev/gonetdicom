package dicomweb

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// StoreResult summarizes a STOW-RS response.
type StoreResult struct {
	StatusCode int
	Body       []byte
	Referenced []*godicom.Dataset // parsed when Content-Type is dicom+json
}

// StoreInstances posts Part 10 instances via STOW-RS.
// studyUID may be empty (POST /studies) or set (POST /studies/{StudyInstanceUID}).
func (c *Client) StoreInstances(ctx context.Context, studyUID string, instances [][]byte) (*StoreResult, error) {
	ct, body, err := encodeSTOWBody(instances)
	if err != nil {
		return nil, err
	}
	var url string
	if studyUID == "" {
		url, err = c.resolve("studies")
	} else {
		url, err = c.resolve("studies", studyUID)
	}
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Accept", MediaTypeDICOMJSON)

	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		msg := string(raw)
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("dicomweb: STOW status %s: %s", resp.Status, msg)
	}
	out := &StoreResult{StatusCode: resp.StatusCode, Body: raw}
	if len(raw) > 0 {
		if refs, err := dicomjson.ParseDatasets(raw); err == nil {
			out.Referenced = refs
		}
	}
	return out, nil
}

// StoreFiles encodes FileDatasets as Part 10 and stores them.
func (c *Client) StoreFiles(ctx context.Context, studyUID string, files []*godicom.FileDataset) (*StoreResult, error) {
	parts := make([][]byte, 0, len(files))
	for i, fd := range files {
		raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
		if err != nil {
			return nil, fmt.Errorf("dicomweb: encode file[%d]: %w", i, err)
		}
		parts = append(parts, raw)
	}
	return c.StoreInstances(ctx, studyUID, parts)
}
