package dicomweb

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// Store is the origin-server backing store for the Studies Service.
type Store interface {
	// GetInstance returns Part 10 bytes for the given UIDs.
	GetInstance(studyUID, seriesUID, instanceUID string) ([]byte, error)
	// ListInstances returns Part 10 bytes for a study (seriesUID empty) or series.
	ListInstances(studyUID, seriesUID string) ([][]byte, error)
	// PutInstance stores a Part 10 instance; studyUID may be empty.
	PutInstance(studyUID string, part10 []byte) error
	// SearchStudies returns matching study-level datasets for QIDO-RS.
	SearchStudies(query url.Values) ([]*godicom.Dataset, error)
	// SearchSeries returns series-level datasets under a study.
	SearchSeries(studyUID string, query url.Values) ([]*godicom.Dataset, error)
	// SearchInstances returns instance-level datasets under a study/series.
	// seriesUID may be empty to search all series in the study.
	SearchInstances(studyUID, seriesUID string, query url.Values) ([]*godicom.Dataset, error)
}

// MemoryStore is an in-memory Store for tests and demos.
type MemoryStore struct {
	mu        sync.RWMutex
	instances map[string][]byte // study/series/instance -> Part 10
	meta      map[string]*godicom.Dataset
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		instances: make(map[string][]byte),
		meta:      make(map[string]*godicom.Dataset),
	}
}

func instanceKey(study, series, instance string) string {
	return study + "/" + series + "/" + instance
}

func (s *MemoryStore) GetInstance(studyUID, seriesUID, instanceUID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.instances[instanceKey(studyUID, seriesUID, instanceUID)]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

func (s *MemoryStore) ListInstances(studyUID, seriesUID string) ([][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := studyUID + "/"
	if seriesUID != "" {
		prefix = studyUID + "/" + seriesUID + "/"
	}
	keys := make([]string, 0)
	for key := range s.instances {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, ErrNotFound
	}
	sort.Strings(keys)
	out := make([][]byte, 0, len(keys))
	for _, key := range keys {
		raw := s.instances[key]
		cp := make([]byte, len(raw))
		copy(cp, raw)
		out = append(out, cp)
	}
	return out, nil
}

func (s *MemoryStore) PutInstance(studyUID string, part10 []byte) error {
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return fmt.Errorf("dicomweb: invalid DICOM part: %w", err)
	}
	study, _ := fd.GetString(godicom.MustTag("StudyInstanceUID"))
	series, _ := fd.GetString(godicom.MustTag("SeriesInstanceUID"))
	instance, _ := fd.GetString(godicom.MustTag("SOPInstanceUID"))
	if study == "" || series == "" || instance == "" {
		return fmt.Errorf("dicomweb: instance missing Study/Series/SOP Instance UID")
	}
	if studyUID != "" && studyUID != study {
		return fmt.Errorf("dicomweb: StudyInstanceUID mismatch: body %s path %s", study, studyUID)
	}
	key := instanceKey(study, series, instance)
	raw := make([]byte, len(part10))
	copy(raw, part10)

	meta := godicom.NewDataset()
	for _, tagName := range []string{
		"SOPClassUID", "SOPInstanceUID",
		"StudyInstanceUID", "SeriesInstanceUID",
		"PatientID", "PatientName", "StudyDate", "Modality",
		"SeriesNumber", "InstanceNumber",
	} {
		if elem, ok := fd.Get(godicom.MustTag(tagName)); ok {
			meta.Set(elem)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[key] = raw
	s.meta[key] = meta
	return nil
}

func (s *MemoryStore) SearchStudies(query url.Values) ([]*godicom.Dataset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[string]*godicom.Dataset{}
	for key, meta := range s.meta {
		study := uidFromMeta(meta, key, 0, "StudyInstanceUID")
		if !matchQuery(meta, query, "PatientID", "StudyInstanceUID", "PatientName", "StudyDate") {
			continue
		}
		if _, ok := seen[study]; ok {
			continue
		}
		seen[study] = projectMeta(meta, "StudyInstanceUID", "PatientID", "PatientName", "StudyDate")
	}
	return mapValues(seen), nil
}

func (s *MemoryStore) SearchSeries(studyUID string, query url.Values) ([]*godicom.Dataset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[string]*godicom.Dataset{}
	for key, meta := range s.meta {
		study := uidFromMeta(meta, key, 0, "StudyInstanceUID")
		if studyUID != "" && study != studyUID {
			continue
		}
		if !matchQuery(meta, query, "SeriesInstanceUID", "Modality", "SeriesNumber") {
			continue
		}
		series := uidFromMeta(meta, key, 1, "SeriesInstanceUID")
		if _, ok := seen[series]; ok {
			continue
		}
		ds := projectMeta(meta, "StudyInstanceUID", "SeriesInstanceUID", "Modality", "SeriesNumber")
		seen[series] = ds
	}
	return mapValues(seen), nil
}

func (s *MemoryStore) SearchInstances(studyUID, seriesUID string, query url.Values) ([]*godicom.Dataset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*godicom.Dataset
	for key, meta := range s.meta {
		study := uidFromMeta(meta, key, 0, "StudyInstanceUID")
		series := uidFromMeta(meta, key, 1, "SeriesInstanceUID")
		if studyUID != "" && study != studyUID {
			continue
		}
		if seriesUID != "" && series != seriesUID {
			continue
		}
		if !matchQuery(meta, query, "SOPInstanceUID", "SOPClassUID", "InstanceNumber", "Modality") {
			continue
		}
		out = append(out, projectMeta(meta,
			"StudyInstanceUID", "SeriesInstanceUID",
			"SOPInstanceUID", "SOPClassUID", "InstanceNumber", "Modality",
		))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i].GetString(godicom.MustTag("SOPInstanceUID"))
		b, _ := out[j].GetString(godicom.MustTag("SOPInstanceUID"))
		return a < b
	})
	return out, nil
}

func uidFromMeta(meta *godicom.Dataset, key string, part int, tagName string) string {
	if v, ok := meta.GetString(godicom.MustTag(tagName)); ok && v != "" {
		return v
	}
	parts := strings.SplitN(key, "/", 3)
	if part >= 0 && part < len(parts) {
		return parts[part]
	}
	return ""
}

func matchQuery(meta *godicom.Dataset, query url.Values, keys ...string) bool {
	for _, key := range keys {
		want := query.Get(key)
		if want == "" {
			continue
		}
		got, _ := meta.GetString(godicom.MustTag(key))
		if got != want {
			return false
		}
	}
	return true
}

func projectMeta(meta *godicom.Dataset, tags ...string) *godicom.Dataset {
	ds := godicom.NewDataset()
	for _, tagName := range tags {
		if elem, ok := meta.Get(godicom.MustTag(tagName)); ok {
			ds.Set(elem)
		}
	}
	return ds
}

func mapValues(m map[string]*godicom.Dataset) []*godicom.Dataset {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*godicom.Dataset, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// ErrNotFound is returned when a resource is missing.
var ErrNotFound = fmt.Errorf("dicomweb: not found")

// Handler returns an http.Handler for the Studies Service under the given prefix
// (e.g. "/dicom-web" or ""). Trailing slashes are ignored.
func Handler(store Store, prefix string) http.Handler {
	prefix = strings.TrimRight(prefix, "/")
	mux := http.NewServeMux()

	mux.HandleFunc(prefix+"/studies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			matches, err := store.SearchStudies(r.URL.Query())
			writeQIDO(w, matches, err)
		case http.MethodPost:
			handleSTOW(w, r, store, "")
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc(prefix+"/studies/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix+"/studies/")
		parts := splitPath(path)
		switch {
		case r.Method == http.MethodPost && len(parts) == 1:
			handleSTOW(w, r, store, parts[0])

		// GET /studies/{study}
		case r.Method == http.MethodGet && len(parts) == 1:
			handleWADOMany(w, r, store, parts[0], "")
		// GET /studies/{study}/metadata
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "metadata":
			handleWADOManyMetadata(w, r, store, parts[0], "")
		// GET /studies/{study}/series
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "series":
			matches, err := store.SearchSeries(parts[0], r.URL.Query())
			writeQIDO(w, matches, err)
		// GET /studies/{study}/instances
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "instances":
			matches, err := store.SearchInstances(parts[0], "", r.URL.Query())
			writeQIDO(w, matches, err)
		// GET /studies/{study}/series/{series}
		case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "series":
			handleWADOMany(w, r, store, parts[0], parts[2])
		// GET /studies/{study}/series/{series}/metadata
		case r.Method == http.MethodGet && len(parts) == 4 && parts[1] == "series" && parts[3] == "metadata":
			handleWADOManyMetadata(w, r, store, parts[0], parts[2])
		// GET /studies/{study}/series/{series}/instances
		case r.Method == http.MethodGet && len(parts) == 4 && parts[1] == "series" && parts[3] == "instances":
			matches, err := store.SearchInstances(parts[0], parts[2], r.URL.Query())
			writeQIDO(w, matches, err)
		// GET /studies/{study}/series/{series}/instances/{instance}
		case r.Method == http.MethodGet && len(parts) == 5 && parts[1] == "series" && parts[3] == "instances":
			handleWADOInstance(w, r, store, parts[0], parts[2], parts[4])
		// GET .../instances/{instance}/metadata
		case r.Method == http.MethodGet && len(parts) == 6 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "metadata":
			handleWADOMetadata(w, r, store, parts[0], parts[2], parts[4])
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func writeQIDO(w http.ResponseWriter, matches []*godicom.Dataset, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(matches) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, err := dicomjson.MarshalDatasets(matches)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	_, _ = w.Write(body)
}

func handleSTOW(w http.ResponseWriter, r *http.Request, store Store, studyUID string) {
	parts, err := readDICOMParts(r.Body, r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var refs []*godicom.Dataset
	for _, part := range parts {
		if err := store.PutInstance(studyUID, part); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fd, err := godicom.ReadBytes(part, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ref := godicom.NewDataset()
		if v, ok := fd.GetString(godicom.MustTag("SOPClassUID")); ok {
			ref.Set(godicom.NewDataElement(godicom.MustTag("ReferencedSOPClassUID"), godicom.VRUI, v))
		}
		if v, ok := fd.GetString(godicom.MustTag("SOPInstanceUID")); ok {
			ref.Set(godicom.NewDataElement(godicom.MustTag("ReferencedSOPInstanceUID"), godicom.VRUI, v))
		}
		refs = append(refs, ref)
	}
	body, err := dicomjson.MarshalDatasets(refs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func handleWADOMany(w http.ResponseWriter, r *http.Request, store Store, study, series string) {
	parts, err := store.ListInstances(study, series)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	boundary := "gonetdicom-boundary"
	w.Header().Set("Content-Type", multipartContentType(boundary))
	_ = writeDICOMParts(w, boundary, parts)
}

func handleWADOManyMetadata(w http.ResponseWriter, r *http.Request, store Store, study, series string) {
	parts, err := store.ListInstances(study, series)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var metas []*godicom.Dataset
	for _, part := range parts {
		fd, err := godicom.ReadBytes(part, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		meta := fd.Clone()
		meta.Delete(godicom.MustTag("PixelData"))
		metas = append(metas, meta)
	}
	body, err := dicomjson.MarshalDatasets(metas)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	_, _ = w.Write(body)
}

func handleWADOInstance(w http.ResponseWriter, r *http.Request, store Store, study, series, instance string) {
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, MediaTypeMultipart) || accept == "" {
		boundary := "gonetdicom-boundary"
		w.Header().Set("Content-Type", multipartContentType(boundary))
		if err := writeDICOMParts(w, boundary, [][]byte{raw}); err != nil {
			return
		}
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOM)
	_, _ = w.Write(raw)
}

func handleWADOMetadata(w http.ResponseWriter, r *http.Request, store Store, study, series, instance string) {
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fd, err := godicom.ReadBytes(raw, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meta := fd.Clone()
	meta.Delete(godicom.MustTag("PixelData"))
	body, err := dicomjson.MarshalDatasets([]*godicom.Dataset{meta})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	_, _ = w.Write(body)
}
