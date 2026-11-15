package dicomweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/godicom-dev/godicom"
)

// The Non-Patient Instance Service (PS3.18 section 12) serves Instances whose
// IOD has no Patient Information Entity at its root: colour palettes, hanging
// protocols, implant templates, defined procedure protocols and their approvals,
// inventories. They are the objects a viewer ships with rather than the objects a
// patient produced, which is why they hang off their own resource names instead
// of under /studies — and why a clinical archive may not have them at all.
//
// The six names below are the whole resource space of the service. A seventh is
// not a resource with no data; it is not a resource, so it gets the answer PS3.18
// gives for anything the origin server does not serve.
var nonPatientCategories = []string{
	"color-palettes",
	"defined-procedure-protocols",
	"hanging-protocols",
	"implant-templates",
	"inventories",
	"protocol-approvals",
}

// NonPatientCategories returns the resource names of the Non-Patient Instance
// Service (PS3.18 Table 12.1.1-1), in the order the standard lists them.
//
// The returned slice is a copy: it is used to decide which paths a server
// answers, so a caller that sorted or truncated it in place would change which
// resources exist.
func NonPatientCategories() []string {
	out := make([]string, len(nonPatientCategories))
	copy(out, nonPatientCategories)
	return out
}

// NonPatientRetriever is the optional backing store for the Retrieve and Search
// Transactions of the Non-Patient Instance Service.
//
// It is separate from Store on purpose. A clinical archive that has no
// non-patient resources is a normal thing to be; requiring three more methods of
// every Store implementation to say so would make the interface describe a
// service most implementations do not have, and would break every existing
// implementation of it. A Handler whose Store does not implement this interface
// does not serve the service at all: the six paths are not registered, so they
// answer 404 exactly like any other path this server does not have.
type NonPatientRetriever interface {
	// GetNonPatientInstance returns the Part 10 bytes of one instance in the
	// given category, or ErrNotFound.
	GetNonPatientInstance(category, uid string) ([]byte, error)
	// SearchNonPatientInstances returns matching instance-level datasets for the
	// Search Transaction. Matching keys are the store's business: PS3.4 gives
	// each category its own attribute table, and the web layer holds no
	// dictionary to apply it with.
	SearchNonPatientInstances(category string, query url.Values) ([]*godicom.Dataset, error)
}

// NonPatientStore adds the Store Transaction, which PS3.18 12.1 makes optional
// for this service. A store that implements only NonPatientRetriever serves
// retrieval and search and answers POST with 405 — the resource exists and the
// method does not, which is a different statement from "no such resource".
//
// uid is the Instance UID from the request path, or empty when the request
// addressed the collection. A non-empty uid is a claim by the requestor that the
// stored instance has that UID, and a store that cannot honour it must reject the
// part rather than store it somewhere else.
type NonPatientStore interface {
	NonPatientRetriever
	PutNonPatientInstance(category, uid string, part10 []byte) error
}

// validInstanceUID reports whether s can be used as a UID path segment.
//
// The handler checks this itself rather than leaving it to the store. The store
// is third-party code, and a common way to write one is to join the category and
// the UID onto a directory; a segment of ".." would then name the parent
// directory rather than an instance, and the request that did it looks like an
// ordinary retrieve. The check is the UID syntax of PS3.5 9.1 — digits and dots,
// no empty component, at most 64 characters — so nothing legal is lost.
func validInstanceUID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	// A dot is only a separator: at the start it would make the segment relative,
	// and next to another one (or at the end) it would name a component that is
	// not there.
	afterDot := true
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			afterDot = false
		case s[i] == '.' && !afterDot && i < len(s)-1:
			afterDot = true
		default:
			return false
		}
	}
	return true
}

// registerNonPatient wires the six resources of the Non-Patient Instance Service
// into mux. See NonPatientRetriever for why a Store without it gets no paths.
//
// The service has exactly two shapes of target: the collection (search it, store
// into it) and one instance (retrieve it, store at it). There is no /metadata,
// /rendered or /thumbnail resource here — Table 12.4.1-1 lists the instance
// alone — so a request for one is a path this server does not have, which is the
// same answer it gives for anything else it does not serve.
func registerNonPatient(mux *http.ServeMux, store Store, prefix string, maxBytes int64) {
	retriever, ok := store.(NonPatientRetriever)
	if !ok {
		return
	}
	storer, _ := store.(NonPatientStore)

	for _, name := range nonPatientCategories {
		// All Instances: Search (GET) and Store (POST).
		mux.HandleFunc(prefix+"/"+name, func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				matches, err := retriever.SearchNonPatientInstances(name, r.URL.Query())
				writeQIDO(w, r, matches, err)
			case http.MethodPost:
				if storer == nil {
					rejectStore(w)
					return
				}
				handleNPIStore(w, r, storer, name, "", maxBytes)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})

		// Instance: Retrieve (GET) and Store (POST).
		mux.HandleFunc(prefix+"/"+name+"/", func(w http.ResponseWriter, r *http.Request) {
			uid := strings.TrimPrefix(r.URL.Path, prefix+"/"+name+"/")
			uid = strings.Trim(uid, "/")
			if !validInstanceUID(uid) {
				http.NotFound(w, r)
				return
			}
			switch r.Method {
			case http.MethodGet:
				handleNPIRetrieve(w, r, retriever, name, uid)
			case http.MethodPost:
				if storer == nil {
					rejectStore(w)
					return
				}
				handleNPIStore(w, r, storer, name, uid, maxBytes)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	}
}

// rejectStore answers a Store request on a store that implements retrieval and
// search but not storing. The resource exists and the method does not, which 405
// plus Allow states; 404 would tell the user agent the category is missing.
func rejectStore(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "store not supported for this resource", http.StatusMethodNotAllowed)
}

func handleNPIRetrieve(w http.ResponseWriter, r *http.Request, store NonPatientRetriever, category, uid string) {
	raw, err := store.GetNonPatientInstance(category, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		respondError(w, r, "npi: get instance", "retrieve failed", http.StatusInternalServerError, err)
		return
	}
	// application/dicom is the Required media type for this service (PS3.18
	// Table 12.1.3-1), so it is the default rather than something to ask for. A
	// user agent that needs the multipart framing the Studies Service returns by
	// default can still ask for it.
	if strings.Contains(r.Header.Get("Accept"), MediaTypeMultipart) {
		_ = writeDICOMParts(w, [][]byte{raw})
		return
	}
	w.Header().Set("Content-Type", MediaTypeDICOM)
	_, _ = w.Write(raw)
}

func handleNPIStore(w http.ResponseWriter, r *http.Request, store NonPatientStore, category, uid string, maxBytes int64) {
	storeInstances(w, r, "npi: put instance", maxBytes, func(part []byte) error {
		return store.PutNonPatientInstance(category, uid, part)
	})
}

// RetrieveNonPatientInstance fetches one non-patient instance as Part 10 bytes.
//
// category is one of NonPatientCategories(); it is sent as given rather than
// checked against that list, because a user agent that refuses a name before
// asking cannot talk to an origin server that implements a category added after
// it was written. An unknown name comes back as the origin server's 404.
func (c *Client) RetrieveNonPatientInstance(ctx context.Context, category, uid string) ([]byte, error) {
	if uid == "" {
		return nil, fmt.Errorf("dicomweb: instance UID required")
	}
	url, err := c.resolve(category, uid)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", MediaTypeDICOM)

	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	// The request asked for a single part, which is what the Required media type
	// of this service is. An origin server that wraps a single instance in a
	// one-part multipart anyway is still answering the question, so the answer is
	// read rather than rejected — the alternative is a client that works with
	// every server except the ones that are only slightly different.
	if strings.HasPrefix(resp.Header.Get("Content-Type"), MediaTypeMultipart) {
		parts, err := readDICOMParts(capReader(resp.Body, c.maxResponseBytes()), resp.Header.Get("Content-Type"))
		if err != nil {
			return nil, err
		}
		if len(parts) != 1 {
			return nil, fmt.Errorf("dicomweb: expected 1 instance part, got %d", len(parts))
		}
		return parts[0], nil
	}
	return readAll(resp.Body, c.maxResponseBytes())
}

// RetrieveNonPatientInstanceFile is RetrieveNonPatientInstance plus
// godicom.ReadBytes.
func (c *Client) RetrieveNonPatientInstanceFile(ctx context.Context, category, uid string) (*godicom.FileDataset, error) {
	raw, err := c.RetrieveNonPatientInstance(ctx, category, uid)
	if err != nil {
		return nil, err
	}
	return godicom.ReadBytes(raw, nil)
}

// SearchNonPatientInstances performs the Search Transaction on one category.
func (c *Client) SearchNonPatientInstances(ctx context.Context, category string, query url.Values) ([]*godicom.Dataset, error) {
	path, err := c.resolve(category)
	if err != nil {
		return nil, err
	}
	return c.search(ctx, path, query)
}

// StoreNonPatientInstances posts Part 10 instances to one category
// (POST /{npi-name}).
func (c *Client) StoreNonPatientInstances(ctx context.Context, category string, instances [][]byte) (*StoreResult, error) {
	url, err := c.resolve(category)
	if err != nil {
		return nil, err
	}
	return c.storeInstances(ctx, url, instances)
}

// StoreNonPatientInstance posts one instance to POST /{npi-name}/{uid}, where the
// UID is stated by the requestor instead of by the instance. An origin server
// that cannot store the instance under that UID is required to reject it rather
// than file it somewhere else, so a success here means the retrieve URL works.
func (c *Client) StoreNonPatientInstance(ctx context.Context, category, uid string, instance []byte) (*StoreResult, error) {
	if uid == "" {
		return nil, fmt.Errorf("dicomweb: instance UID required")
	}
	url, err := c.resolve(category, uid)
	if err != nil {
		return nil, err
	}
	return c.storeInstances(ctx, url, [][]byte{instance})
}

// StoreNonPatientFiles encodes FileDatasets as Part 10 and stores them.
func (c *Client) StoreNonPatientFiles(ctx context.Context, category string, files []*godicom.FileDataset) (*StoreResult, error) {
	parts := make([][]byte, 0, len(files))
	for i, fd := range files {
		raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
		if err != nil {
			return nil, fmt.Errorf("dicomweb: encode file[%d]: %w", i, err)
		}
		parts = append(parts, raw)
	}
	return c.StoreNonPatientInstances(ctx, category, parts)
}
