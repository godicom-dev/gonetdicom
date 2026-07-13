package dicomweb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// SearchStudies performs QIDO-RS Search for Studies.
// query keys are DICOM attribute keywords or tag numbers as used by the origin server
// (e.g. PatientID, StudyInstanceUID).
func (c *Client) SearchStudies(ctx context.Context, query url.Values) ([]*godicom.Dataset, error) {
	base, err := c.resolve("studies")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", MediaTypeDICOMJSON)

	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	datasets, err := dicomjson.ParseDatasets(body)
	if err != nil {
		return nil, fmt.Errorf("dicomweb: parse QIDO response: %w", err)
	}
	return datasets, nil
}
