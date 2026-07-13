package dicomweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/dicomjson"
)

// Store is the origin-server backing store for the Studies Service MVP.
type Store interface {
	// GetInstance returns Part 10 bytes for the given UIDs.
	GetInstance(studyUID, seriesUID, instanceUID string) ([]byte, error)
	// PutInstance stores a Part 10 instance; studyUID may be empty.
	PutInstance(studyUID string, part10 []byte) error
	// SearchStudies returns matching study-level datasets for QIDO-RS.
	SearchStudies(query url.Values) ([]*godicom.Dataset, error)
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
		"PatientID", "PatientName", "StudyDate", "ModalitiesInStudy",
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

	wantPatient := query.Get("PatientID")
	wantStudy := query.Get("StudyInstanceUID")

	seen := map[string]*godicom.Dataset{}
	for key, meta := range s.meta {
		study, _ := meta.GetString(godicom.MustTag("StudyInstanceUID"))
		if study == "" {
			parts := strings.SplitN(key, "/", 3)
			if len(parts) > 0 {
				study = parts[0]
			}
		}
		if wantStudy != "" && study != wantStudy {
			continue
		}
		if wantPatient != "" {
			pid, _ := meta.GetString(godicom.MustTag("PatientID"))
			if pid != wantPatient {
				continue
			}
		}
		if _, ok := seen[study]; ok {
			continue
		}
		ds := godicom.NewDataset()
		for _, tagName := range []string{"StudyInstanceUID", "PatientID", "PatientName", "StudyDate"} {
			if elem, ok := meta.Get(godicom.MustTag(tagName)); ok {
				ds.Set(elem)
			}
		}
		if ds.Len() == 0 {
			ds.Set(godicom.NewDataElement(godicom.MustTag("StudyInstanceUID"), godicom.VRUI, study))
		}
		seen[study] = ds
	}
	out := make([]*godicom.Dataset, 0, len(seen))
	for _, ds := range seen {
		out = append(out, ds)
	}
	return out, nil
}

// ErrNotFound is returned when a resource is missing.
var ErrNotFound = fmt.Errorf("dicomweb: not found")

// Handler returns an http.Handler for the Studies Service MVP under the given prefix
// (e.g. "/dicom-web" or ""). Trailing slashes are ignored.
func Handler(store Store, prefix string) http.Handler {
	prefix = strings.TrimRight(prefix, "/")
	mux := http.NewServeMux()

	mux.HandleFunc(prefix+"/studies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleQIDOStudies(w, r, store)
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
		case r.Method == http.MethodGet && len(parts) == 5 && parts[1] == "series" && parts[3] == "instances":
			handleWADOInstance(w, r, store, parts[0], parts[2], parts[4])
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

func handleQIDOStudies(w http.ResponseWriter, r *http.Request, store Store) {
	matches, err := store.SearchStudies(r.URL.Query())
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

func handleWADOInstance(w http.ResponseWriter, r *http.Request, store Store, study, series, instance string) {
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if err == ErrNotFound {
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
		if err == ErrNotFound {
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
	// Strip Pixel Data for metadata.
	meta := fd.Dataset.Clone()
	meta.Delete(godicom.MustTag("PixelData"))
	body, err := dicomjson.MarshalDatasets([]*godicom.Dataset{meta})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	_, _ = w.Write(body)
}