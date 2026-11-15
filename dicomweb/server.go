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
//
// The three Search methods receive the request's query parameters verbatim,
// because which attributes a level matches on and which of them it can answer
// with are the store's knowledge, not the web layer's: the handler holds no data
// dictionary. That includes the parameters about the answer rather than the
// match — limit, offset, fuzzymatching and includefield — which are
// parseQIDOOptions' terms and MemoryStore's to honour; a store that cannot
// honour one returns ErrBadQuery and the request is answered 400 rather than
// with a different answer than the one asked for.
type Store interface {
	// GetInstance returns Part 10 bytes for the given UIDs.
	GetInstance(studyUID, seriesUID, instanceUID string) ([]byte, error)
	// ListInstances returns Part 10 bytes for a study (seriesUID empty) or series.
	ListInstances(studyUID, seriesUID string) ([][]byte, error)
	// PutInstance stores a Part 10 instance; studyUID may be empty.
	PutInstance(studyUID string, part10 []byte) error
	// SearchStudies returns matching study-level datasets for QIDO-RS.
	SearchStudies(query url.Values) ([]*godicom.Dataset, error)
	// SearchSeries returns series-level datasets for QIDO-RS.
	//
	// studyUID may be empty, which is the Search target "All Series"
	// (GET /series) rather than a study named by the empty string: every series
	// of every study the store holds. An implementation that reads a query as
	// "under the study I was given" and so returns nothing for "" makes the
	// server answer 204 to a request it could have answered, which looks like an
	// empty archive instead of an unsupported one.
	SearchSeries(studyUID string, query url.Values) ([]*godicom.Dataset, error)
	// SearchInstances returns instance-level datasets for QIDO-RS.
	//
	// Both UIDs may be empty, and the empty ones are the absent levels of the
	// target rather than levels named by an empty string: seriesUID empty means
	// every series of the study, studyUID empty means every study (the Search
	// target "All Instances", GET /instances).
	SearchInstances(studyUID, seriesUID string, query url.Values) ([]*godicom.Dataset, error)
}

// MemoryStore is an in-memory Store for tests and demos.
//
// It also serves the Non-Patient Instance Service: instances stored under a
// category are kept apart from study instances, because the same SOP Instance UID
// may legitimately be a colour palette and part of nothing at all.
type MemoryStore struct {
	mu        sync.RWMutex
	instances map[string][]byte // study/series/instance -> Part 10
	meta      map[string]*godicom.Dataset
	npi       map[string][]byte // category/instance -> Part 10
	npiMeta   map[string]*godicom.Dataset
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		instances: make(map[string][]byte),
		meta:      make(map[string]*godicom.Dataset),
		npi:       make(map[string][]byte),
		npiMeta:   make(map[string]*godicom.Dataset),
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

	// The whole dataset, not a fixed list of searchable attributes: a Search
	// response may carry any attribute the requestor names with includefield,
	// and a store that indexed four of them could not answer that. This is the
	// reference store — an archive with a real database indexes what it needs
	// and answers includefield from the query — so it keeps what it was given.
	meta := fd.Clone()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances[key] = raw
	s.meta[key] = meta
	return nil
}

// npiKey is the storage key for a non-patient instance. The category is part of
// it: a retrieve for /hanging-protocols/{uid} must not be answered by a colour
// palette that happens to have the same UID.
func npiKey(category, uid string) string {
	return category + "/" + uid
}

// GetNonPatientInstance returns a copy, so a caller cannot rewrite what is stored
// by writing to what it was given.
func (s *MemoryStore) GetNonPatientInstance(category, uid string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.npi[npiKey(category, uid)]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

func (s *MemoryStore) PutNonPatientInstance(category, uid string, part10 []byte) error {
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return fmt.Errorf("dicomweb: invalid DICOM part: %w", err)
	}
	instance, _ := fd.GetString(godicom.MustTag("SOPInstanceUID"))
	if instance == "" {
		return fmt.Errorf("dicomweb: instance missing SOP Instance UID")
	}
	// A UID in the path is a claim about the instance being stored, not a second
	// name for it: storing the body under the path UID would file an instance
	// where its own retrieve URL can never find it again.
	if uid != "" && uid != instance {
		return fmt.Errorf("dicomweb: SOPInstanceUID mismatch: body %s path %s", instance, uid)
	}
	key := npiKey(category, instance)
	raw := make([]byte, len(part10))
	copy(raw, part10)

	// Every element except the bulk ones. PS3.4 gives each of the six categories
	// its own search attributes, and this package does not carry those tables — so
	// rather than index a fixed handful of keys and silently fail to match the
	// rest, the demo store keeps what it can afford to and lets
	// matchNonPatientQuery compare whatever the caller asks for.
	meta := godicom.NewDataset()
	for _, elem := range fd.Iter() {
		if godicom.BytesVR[elem.VR] {
			continue
		}
		meta.Set(elem)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.npi[key] = raw
	s.npiMeta[key] = meta
	return nil
}

func (s *MemoryStore) SearchNonPatientInstances(category string, query url.Values) ([]*godicom.Dataset, error) {
	opts, err := parseQIDOOptions(query)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := category + "/"
	out := make([]*godicom.Dataset, 0)
	for key, meta := range s.npiMeta {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if !matchNonPatientQuery(meta, query) {
			continue
		}
		out = append(out, meta.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i].GetString(godicom.MustTag("SOPInstanceUID"))
		b, _ := out[j].GetString(godicom.MustTag("SOPInstanceUID"))
		return a < b
	})
	return page(out, opts), nil
}

// matchNonPatientQuery compares a stored instance against the query parameters of
// a Search request.
//
// A keyword this store cannot resolve is ignored, and one the instance does not
// carry is not a match: asking whether an instance has an attribute it does not
// have is answered by the instance, not by the query parser.
func matchNonPatientQuery(meta *godicom.Dataset, query url.Values) bool {
	for key := range query {
		want := query.Get(key)
		if want == "" {
			continue
		}
		tag, err := godicom.ParseTag(key)
		if err != nil {
			continue
		}
		got, ok := meta.GetString(tag)
		if !ok || got != want {
			return false
		}
	}
	return true
}

func (s *MemoryStore) SearchStudies(query url.Values) ([]*godicom.Dataset, error) {
	opts, err := parseQIDOOptions(query)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Sorted keys, because a study's response may now carry the whole stored
	// dataset (includefield=all) and the first instance seen would otherwise
	// decide which one a caller gets — a map iteration, i.e. a different answer
	// per run.
	seen := map[string]*godicom.Dataset{}
	for _, key := range sortedKeys(s.meta) {
		meta := s.meta[key]
		study := uidFromMeta(meta, key, 0, "StudyInstanceUID")
		if !matchQuery(meta, query, opts.fuzzy, "PatientID", "StudyInstanceUID", "PatientName", "StudyDate") {
			continue
		}
		if _, ok := seen[study]; ok {
			continue
		}
		seen[study] = project(meta, opts, "StudyInstanceUID", "PatientID", "PatientName", "StudyDate")
	}
	return page(mapValues(seen), opts), nil
}

func (s *MemoryStore) SearchSeries(studyUID string, query url.Values) ([]*godicom.Dataset, error) {
	opts, err := parseQIDOOptions(query)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[string]*godicom.Dataset{}
	for _, key := range sortedKeys(s.meta) {
		meta := s.meta[key]
		study := uidFromMeta(meta, key, 0, "StudyInstanceUID")
		if studyUID != "" && study != studyUID {
			continue
		}
		// StudyInstanceUID is a query key as well as a level: in the All Series
		// target there is no study in the URI, so a requestor that wants one
		// study's series against /series asks for it here. Ignoring the key
		// would answer with every series in the archive, which is a superset the
		// requestor has no way to notice.
		if !matchQuery(meta, query, opts.fuzzy, "StudyInstanceUID", "SeriesInstanceUID", "Modality", "SeriesNumber") {
			continue
		}
		series := uidFromMeta(meta, key, 1, "SeriesInstanceUID")
		if _, ok := seen[series]; ok {
			continue
		}
		seen[series] = project(meta, opts, "StudyInstanceUID", "SeriesInstanceUID", "Modality", "SeriesNumber")
	}
	return page(mapValues(seen), opts), nil
}

func (s *MemoryStore) SearchInstances(studyUID, seriesUID string, query url.Values) ([]*godicom.Dataset, error) {
	opts, err := parseQIDOOptions(query)
	if err != nil {
		return nil, err
	}
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
		// The levels above the instance are query keys too: in the All Instances
		// target neither the study nor the series is in the URI, so /instances
		// with StudyInstanceUID or SeriesInstanceUID is how a requestor asks for
		// one parent's instances. Dropping those keys would answer with the whole
		// archive.
		if !matchQuery(meta, query, opts.fuzzy,
			"StudyInstanceUID", "SeriesInstanceUID",
			"SOPInstanceUID", "SOPClassUID", "InstanceNumber", "Modality",
		) {
			continue
		}
		out = append(out, project(meta, opts,
			"StudyInstanceUID", "SeriesInstanceUID",
			"SOPInstanceUID", "SOPClassUID", "InstanceNumber", "Modality",
		))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i].GetString(godicom.MustTag("SOPInstanceUID"))
		b, _ := out[j].GetString(godicom.MustTag("SOPInstanceUID"))
		return a < b
	})
	return page(out, opts), nil
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

// matchQuery compares a stored dataset against the query parameters of a Search
// request.
//
// The keys are the attributes the level answers by, and the query parser has
// already been through the ones that are about the answer rather than the match
// (see parseQIDOOptions). fuzzy makes the free-text VRs match on a
// case-insensitive substring, which is what 8.3.4.3 asks for; the other VRs are
// compared exactly either way.
func matchQuery(meta *godicom.Dataset, query url.Values, fuzzy bool, keys ...string) bool {
	for _, key := range keys {
		want := query.Get(key)
		if want == "" {
			continue
		}
		tag := godicom.MustTag(key)
		elem, ok := meta.Get(tag)
		if !ok {
			return false
		}
		got, ok := meta.GetString(tag)
		if !ok {
			return false
		}
		if got == want {
			continue
		}
		if fuzzy && fuzzyVR(elem.VR) && strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			continue
		}
		return false
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

// sortedKeys returns a map's keys in order, for the loops that have to pick one
// dataset out of several that describe the same entity.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
		// GET /studies/{study}/rendered
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "rendered":
			handleWADORenderedMany(w, r, store, parts[0], "")
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
		// GET /studies/{study}/series/{series}/rendered
		case r.Method == http.MethodGet && len(parts) == 4 && parts[1] == "series" && parts[3] == "rendered":
			handleWADORenderedMany(w, r, store, parts[0], parts[2])
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
		// GET .../instances/{instance}/frames/{frames}
		case r.Method == http.MethodGet && len(parts) == 7 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "frames":
			handleWADOFrames(w, r, store, parts[0], parts[2], parts[4], parts[6])
		default:
			http.NotFound(w, r)
		}
	})
	// The two top-level Search targets, All Series (GET /series) and All
	// Instances (GET /instances). They are the same transaction as the nested
	// ones with the enclosing levels left out, and the levels are left out the
	// way a URI leaves them out — not by naming a study or series with the empty
	// string. Search is the only transaction PS3.18 10.3 puts on these two: the
	// Store Transaction is defined on /studies and /studies/{study}, so POST
	// here is a method that does not exist rather than one this server omits.
	mux.HandleFunc(prefix+"/series", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		matches, err := store.SearchSeries("", r.URL.Query())
		writeQIDO(w, r, matches, err)
	})
	mux.HandleFunc(prefix+"/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		matches, err := store.SearchInstances("", "", r.URL.Query())
		writeQIDO(w, r, matches, err)
	})

	registerNonPatient(mux, store, prefix, maxBytes)
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
		// A request the store could not honour is the requestor's mistake, not
		// a server fault: PS3.18 8.3.4.2 wants it answered, not hidden behind a
		// 500 that reads as "the archive is broken".
		if errors.Is(err, ErrBadQuery) {
			respondError(w, r, "qido: search", err.Error(), http.StatusBadRequest, err)
			return
		}
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
	storeInstances(w, r, "stow: put instance", maxBytes, func(part []byte) error {
		return store.PutInstance(studyUID, part)
	})
}

// storeInstances reads one Store request body and hands each DICOM part to put,
// then answers with the SOP Class / SOP Instance reference list the Store
// Transaction response is made of (PS3.18 Annex I).
//
// The Studies Service and the Non-Patient Instance Service store instances this
// way, and the body bound is the part worth not writing twice: the request is
// unauthenticated and buffered whole, so its size is this server's memory use as
// decided by whoever posted it. Content-Length is checked first because it costs
// nothing and catches the honest client; MaxBytesReader catches the one that
// lies or sends chunked.
func storeInstances(w http.ResponseWriter, r *http.Request, op string, maxBytes int64, put func([]byte) error) {
	body := r.Body
	if maxBytes > 0 {
		if r.ContentLength > maxBytes {
			respondError(w, r, op, "request body too large", http.StatusRequestEntityTooLarge,
				fmt.Errorf("%w: Content-Length %d over %d", ErrTooLarge, r.ContentLength, maxBytes))
			return
		}
		body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	parts, err := readDICOMParts(body, r.Header.Get("Content-Type"))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || errors.Is(err, ErrTooLarge) {
			respondError(w, r, op, "request body too large", http.StatusRequestEntityTooLarge, err)
			return
		}
		// This message describes the request's own Content-Type and multipart
		// framing, which is the requestor's to fix and holds nothing of ours.
		respondError(w, r, op, err.Error(), http.StatusBadRequest, err)
		return
	}
	refs := make([]*godicom.Dataset, 0, len(parts))
	for _, part := range parts {
		if err := put(part); err != nil {
			respondError(w, r, op, "instance rejected", http.StatusBadRequest, err)
			return
		}
		ref, err := sopRef(part)
		if err != nil {
			respondError(w, r, op, "instance rejected", http.StatusBadRequest, err)
			return
		}
		refs = append(refs, ref)
	}
	out, err := dicomjson.MarshalDatasets(refs)
	if err != nil {
		respondError(w, r, op, "store failed", http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOMJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// sopRef builds the (0008,1150) / (0008,1155) pair a Store Transaction response
// reports for one instance. It reads the part rather than asking the Store for
// the UIDs, because the response describes what was sent: a Store that rewrites
// an instance — decompressing it, or assigning a UID — would otherwise make the
// answer describe something other than the instance the response is about.
func sopRef(part []byte) (*godicom.Dataset, error) {
	fd, err := godicom.ReadBytes(part, nil)
	if err != nil {
		return nil, err
	}
	ref := godicom.NewDataset()
	if v, ok := fd.GetString(godicom.MustTag("SOPClassUID")); ok {
		ref.Set(godicom.NewDataElement(godicom.MustTag("ReferencedSOPClassUID"), godicom.VRUI, v))
	}
	if v, ok := fd.GetString(godicom.MustTag("SOPInstanceUID")); ok {
		ref.Set(godicom.NewDataElement(godicom.MustTag("ReferencedSOPInstanceUID"), godicom.VRUI, v))
	}
	return ref, nil
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
		metas = append(metas, metadataDataset(fd))
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
	body, err := marshalInstanceMetadata(metadataDataset(fd), BulkDataURI(prefix, study, series, instance))
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

// handleWADORenderedMany answers Retrieve Rendered for a study (series empty) or
// a series: one part per instance, in the media type the request asked for.
//
// An instance with nothing to render — an SR, an PR, an image whose Pixel Data
// was never sent — is left out instead of failing the response. A study is a
// mixed bag by nature, and the requestor asked for pictures of it rather than for
// it to consist of them; answering 406 for the whole study because one series
// holds a Structured Report would make the transaction useless on real archives.
// What is *not* tolerated is having nothing to send: then the answer is 406, and
// the cause is the first instance's.
func handleWADORenderedMany(w http.ResponseWriter, r *http.Request, store Store, study, series string) {
	parts, err := store.ListInstances(study, series)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: list instances", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	opts := parseRenderOptions(r.Header.Get("Accept"), r.URL.Query())
	rendered := make([][]byte, 0, len(parts))
	var (
		mediaType string
		firstErr  error
	)
	for _, raw := range parts {
		mt, body, err := RenderInstance(raw, opts)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		mediaType = mt
		rendered = append(rendered, body)
	}
	if len(rendered) == 0 {
		// Every stored instance was a property of the resource, not of the
		// request: this is the same 406 an unrenderable single instance gets.
		respondError(w, r, "wado: render instances", "cannot render this resource as requested", http.StatusNotAcceptable, firstErr)
		return
	}
	if skipped := len(parts) - len(rendered); skipped > 0 {
		// The answer is a subset of what was asked for, which the requestor
		// cannot tell from a study that holds this many images. Say so on the
		// server side; the log is where an operator can find out why.
		gonetdicom.LoggerFromContext(r.Context()).
			With(gonetdicom.AttrComponent, gonetdicom.ComponentDICOMweb).
			DebugContext(r.Context(), "wado: rendered resource",
				gonetdicom.AttrMethod, r.Method,
				gonetdicom.AttrURL, r.URL.Path,
				"instances", len(parts),
				"skipped", skipped,
				"err", firstErr)
	}
	_ = writeRenderedParts(w, rendered, mediaType)
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

func handleWADOFrames(w http.ResponseWriter, r *http.Request, store Store, study, series, instance, frameList string) {
	indexes, err := parseFrameList(frameList)
	if err != nil {
		http.Error(w, "bad frame list", http.StatusBadRequest)
		return
	}
	raw, err := store.GetInstance(study, series, instance)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "wado: get instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	transferSyntax, frames, err := ExtractFrames(raw)
	if err != nil {
		if errors.Is(err, errNoPixelData) {
			respondError(w, r, "wado: extract frames", "no bulk data for this instance", http.StatusNotFound, err)
			return
		}
		respondError(w, r, "wado: extract frames", "cannot retrieve frames", http.StatusNotAcceptable, err)
		return
	}
	selected := make([][]byte, 0, len(indexes))
	for _, n := range indexes {
		if n > len(frames) {
			respondError(w, r, "wado: frame index", "cannot retrieve frames", http.StatusNotAcceptable,
				fmt.Errorf("dicomweb: frame %d out of range (have %d)", n, len(frames)))
			return
		}
		selected = append(selected, frames[n-1])
	}
	// A part write can fail only after the multipart preamble is on the wire.
	_ = writeFrameParts(w, selected, transferSyntax)
}
