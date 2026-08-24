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
	"github.com/godicom-dev/gonetdicom"
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

// HandlerOption configures a Handler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	maxRequestBytes int64
}

// maxBytes resolves the request bound: unset means DefaultMaxRequestBytes, and a
// negative value means the caller asked for no bound at all.
func (o handlerOptions) maxBytes() int64 {
	if o.maxRequestBytes == 0 {
		return DefaultMaxRequestBytes
	}
	return o.maxRequestBytes
}

// WithMaxRequestBytes bounds one STOW-RS request body. Zero keeps
// DefaultMaxRequestBytes; a negative n removes the bound, which makes this
// server's memory use a function of what an unauthenticated client chooses to
// post.
func WithMaxRequestBytes(n int64) HandlerOption {
	return func(o *handlerOptions) { o.maxRequestBytes = n }
}

// respondError answers the request and logs why.
//
// msg is all the requestor is told. Handler serves whoever connects to it, while
// the errors here come from a Store implementation, from godicom decoding stored
// bytes, or from the render path — text built out of server state: a filesystem
// path, a DSN, an upstream URL, the contents of a stored instance. Only a message
// this package composed from the request itself is safe to hand back, so the
// detail goes to the log instead.
//
// The log target is the request context's logger, so install one with
// gonetdicom.WithLogger from a middleware or http.Server.BaseContext.
func respondError(w http.ResponseWriter, r *http.Request, op, msg string, code int, err error) {
	gonetdicom.LoggerFromContext(r.Context()).
		With(gonetdicom.AttrComponent, gonetdicom.ComponentDICOMweb).
		ErrorContext(r.Context(), op,
			gonetdicom.AttrMethod, r.Method,
			gonetdicom.AttrURL, r.URL.Path,
			gonetdicom.AttrHTTPStatus, code,
			"err", err)
	http.Error(w, msg, code)
}

// Handler returns an http.Handler for the Studies Service under the given prefix
// (e.g. "/dicom-web" or ""). Trailing slashes are ignored.
//
// Request bodies are bounded (see WithMaxRequestBytes) and an error response
// carries only its status and a fixed reason; the cause is logged. See
// respondError.
func Handler(store Store, prefix string, opts ...HandlerOption) http.Handler {
	prefix = strings.TrimRight(prefix, "/")
	var cfg handlerOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	maxBytes := cfg.maxBytes()
	mux := http.NewServeMux()

	mux.HandleFunc(prefix+"/studies", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			matches, err := store.SearchStudies(r.URL.Query())
			writeQIDO(w, r, matches, err)
		case http.MethodPost:
			handleSTOW(w, r, store, "", maxBytes)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc(prefix+"/studies/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix+"/studies/")
		parts := splitPath(path)
		switch {
		case r.Method == http.MethodPost && len(parts) == 1:
			handleSTOW(w, r, store, parts[0], maxBytes)

		// GET /studies/{study}
		case r.Method == http.MethodGet && len(parts) == 1:
			handleWADOMany(w, r, store, parts[0], "")
		// GET /studies/{study}/metadata
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "metadata":
			handleWADOManyMetadata(w, r, store, prefix, parts[0], "")
		// GET /studies/{study}/series
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "series":
			matches, err := store.SearchSeries(parts[0], r.URL.Query())
			writeQIDO(w, r, matches, err)
		// GET /studies/{study}/instances
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "instances":
			matches, err := store.SearchInstances(parts[0], "", r.URL.Query())
			writeQIDO(w, r, matches, err)
		// GET /studies/{study}/series/{series}
		case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "series":
			handleWADOMany(w, r, store, parts[0], parts[2])
		// GET /studies/{study}/series/{series}/metadata
		case r.Method == http.MethodGet && len(parts) == 4 && parts[1] == "series" && parts[3] == "metadata":
			handleWADOManyMetadata(w, r, store, prefix, parts[0], parts[2])
		// GET /studies/{study}/series/{series}/instances
		case r.Method == http.MethodGet && len(parts) == 4 && parts[1] == "series" && parts[3] == "instances":
			matches, err := store.SearchInstances(parts[0], parts[2], r.URL.Query())
			writeQIDO(w, r, matches, err)
		// GET /studies/{study}/series/{series}/instances/{instance}
		case r.Method == http.MethodGet && len(parts) == 5 && parts[1] == "series" && parts[3] == "instances":
			handleWADOInstance(w, r, store, parts[0], parts[2], parts[4])
		// GET .../instances/{instance}/metadata
		case r.Method == http.MethodGet && len(parts) == 6 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "metadata":
			handleWADOMetadata(w, r, store, prefix, parts[0], parts[2], parts[4])
		// GET .../instances/{instance}/rendered
		case r.Method == http.MethodGet && len(parts) == 6 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "rendered":
			handleWADORendered(w, r, store, parts[0], parts[2], parts[4])
		// GET .../instances/{instance}/bulkdata
		case r.Method == http.MethodGet && len(parts) == 6 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "bulkdata":
			handleWADOBulkData(w, r, store, parts[0], parts[2], parts[4])
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

func writeQIDO(w http.ResponseWriter, r *http.Request, matches []*godicom.Dataset, err error) {
	if err != nil {
		respondError(w, r, "qido: search", "search failed", http.StatusInternalServerError, err)
		return
	}
	if len(matches) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, err := dicomjson.MarshalDatasets(matches)
	if err != nil {
		respondError(w, r, "qido: marshal matches", "search failed", http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	_, _ = w.Write(body)
}

func handleSTOW(w http.ResponseWriter, r *http.Request, store Store, studyUID string, maxBytes int64) {
	// The body is buffered whole and then handed to a Store that usually copies it,
	// so its size is this server's memory use, decided by whoever posted it.
	// Content-Length is checked first because it costs nothing and catches the
	// honest client; MaxBytesReader catches the one that lies or sends chunked.
	body := r.Body
	if maxBytes > 0 {
		if r.ContentLength > maxBytes {
			respondError(w, r, "stow: request body", "request body too large", http.StatusRequestEntityTooLarge,
				fmt.Errorf("%w: Content-Length %d over %d", ErrTooLarge, r.ContentLength, maxBytes))
			return
		}
		body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	parts, err := readDICOMParts(body, r.Header.Get("Content-Type"))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || errors.Is(err, ErrTooLarge) {
			respondError(w, r, "stow: request body", "request body too large", http.StatusRequestEntityTooLarge, err)
			return
		}
		// This message describes the request's own Content-Type and multipart
		// framing, which is the requestor's to fix and holds nothing of ours.
		respondError(w, r, "stow: read parts", err.Error(), http.StatusBadRequest, err)
		return
	}
	var refs []*godicom.Dataset
	for _, part := range parts {
		if err := store.PutInstance(studyUID, part); err != nil {
			respondError(w, r, "stow: put instance", "instance rejected", http.StatusBadRequest, err)
			return
		}
		fd, err := godicom.ReadBytes(part, nil)
		if err != nil {
			respondError(w, r, "stow: read stored instance", "instance rejected", http.StatusBadRequest, err)
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
	out, err := dicomjson.MarshalDatasets(refs)
	if err != nil {
		respondError(w, r, "stow: marshal response", "store failed", http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func handleWADOMany(w http.ResponseWriter, r *http.Request, store Store, study, series string) {
	parts, err := store.ListInstances(study, series)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: list instances", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	_ = writeDICOMParts(w, parts)
}

func handleWADOManyMetadata(w http.ResponseWriter, r *http.Request, store Store, prefix, study, series string) {
	parts, err := store.ListInstances(study, series)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: list instances", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	var metas []*godicom.Dataset
	for _, part := range parts {
		fd, err := godicom.ReadBytes(part, nil)
		if err != nil {
			respondError(w, r, "wado: read stored instance", "retrieve failed", http.StatusInternalServerError, err)
			return
		}
		metas = append(metas, fd.Dataset)
	}
	body, err := marshalManyMetadata(metas, prefix, study, series)
	if err != nil {
		respondError(w, r, "wado: marshal metadata", "retrieve failed", http.StatusInternalServerError, err)
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
		respondError(w, r, "wado: get instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, MediaTypeMultipart) || accept == "" {
		_ = writeDICOMParts(w, [][]byte{raw})
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOM)
	_, _ = w.Write(raw)
}

func handleWADOMetadata(w http.ResponseWriter, r *http.Request, store Store, prefix, study, series, instance string) {
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: get instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	fd, err := godicom.ReadBytes(raw, nil)
	if err != nil {
		respondError(w, r, "wado: read stored instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	body, err := marshalInstanceMetadata(fd.Dataset, BulkDataURI(prefix, study, series, instance))
	if err != nil {
		respondError(w, r, "wado: marshal metadata", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	_, _ = w.Write(body)
}

func handleWADORendered(w http.ResponseWriter, r *http.Request, store Store, study, series, instance string) {
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: get instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	opts := parseRenderOptions(r.Header.Get("Accept"), r.URL.Query())
	mediaType, body, err := RenderInstance(raw, opts)
	if err != nil {
		// The reason is a property of the stored instance — its transfer syntax, its
		// frame count, its photometric interpretation — not of the request.
		respondError(w, r, "wado: render instance", "cannot render this instance as requested", http.StatusNotAcceptable, err)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	_, _ = w.Write(body)
}

func handleWADOBulkData(w http.ResponseWriter, r *http.Request, store Store, study, series, instance string) {
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: get instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	bulk, err := ExtractPixelBulkData(raw)
	if err != nil {
		respondError(w, r, "wado: extract bulk data", "no bulk data for this instance", http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", MediaTypeOctetStream)
	_, _ = w.Write(bulk)
}
