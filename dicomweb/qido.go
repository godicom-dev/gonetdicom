package dicomweb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// SearchStudies performs QIDO-RS Search for Studies.
func (c *Client) SearchStudies(ctx context.Context, query url.Values) ([]*godicom.Dataset, error) {
	path, err := c.resolve("studies")
	if err != nil {
		return nil, err
	}
	return c.search(ctx, path, query)
}

// SearchSeries performs QIDO-RS Search for Series under a study.
func (c *Client) SearchSeries(ctx context.Context, studyUID string, query url.Values) ([]*godicom.Dataset, error) {
	if studyUID == "" {
		return nil, fmt.Errorf("dicomweb: study UID required")
	}
	path, err := c.resolve("studies", studyUID, "series")
	if err != nil {
		return nil, err
	}
	return c.search(ctx, path, query)
}

// SearchAllSeries performs QIDO-RS Search for Series with no study above it
// (GET /series), the target PS3.18 calls "All Series".
//
// This is a separate method rather than SearchSeries with an empty UID: a study
// UID that arrives empty from a variable is a bug, and turning that bug into
// "return everything" would answer with the archive instead of refusing.
func (c *Client) SearchAllSeries(ctx context.Context, query url.Values) ([]*godicom.Dataset, error) {
	path, err := c.resolve("series")
	if err != nil {
		return nil, err
	}
	return c.search(ctx, path, query)
}

// SearchAllInstances performs QIDO-RS Search for Instances with neither study
// nor series above it (GET /instances), the target PS3.18 calls
// "All Instances". See SearchAllSeries for why the absent levels are a method of
// their own instead of empty arguments.
func (c *Client) SearchAllInstances(ctx context.Context, query url.Values) ([]*godicom.Dataset, error) {
	path, err := c.resolve("instances")
	if err != nil {
		return nil, err
	}
	return c.search(ctx, path, query)
}

// SearchInstances performs QIDO-RS Search for Instances under a study/series.
// seriesUID may be empty to search all instances in the study
// (GET /studies/{study}/instances).
func (c *Client) SearchInstances(ctx context.Context, studyUID, seriesUID string, query url.Values) ([]*godicom.Dataset, error) {
	if studyUID == "" {
		return nil, fmt.Errorf("dicomweb: study UID required")
	}
	var (
		path string
		err  error
	)
	if seriesUID == "" {
		path, err = c.resolve("studies", studyUID, "instances")
	} else {
		path, err = c.resolve("studies", studyUID, "series", seriesUID, "instances")
	}
	if err != nil {
		return nil, err
	}
	return c.search(ctx, path, query)
}

func (c *Client) search(ctx context.Context, path string, query url.Values) ([]*godicom.Dataset, error) {
	u, err := url.Parse(path)
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
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, http.StatusOK, http.StatusNoContent); err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	body, err := readAll(resp.Body, c.maxResponseBytes())
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
