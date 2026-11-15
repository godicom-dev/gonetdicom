package dicomweb

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/godicom-dev/godicom"
)

// ErrBadQuery marks a Search request whose parameters cannot be honoured: a
// limit that is not a number, an offset before the first match, an includefield
// that names no attribute, a fuzzymatching that is not a boolean.
//
// It is a distinct error because the answer is 400, not 500: the store is
// telling the server that the request was wrong, and PS3.18 8.3.4.2 has a
// requestor that asks about an attribute it cannot have be told so rather than
// served a silently narrower answer. A store may return it from any Search
// method, and Handler turns it into that status.
var ErrBadQuery = errors.New("dicomweb: unsupported search parameter")

// qidoOptions are the Search parameters that are about the answer rather than
// about matching: how many matches to return, where to start, whether string
// matching is fuzzy, and which attributes the response carries beyond the
// level's own.
type qidoOptions struct {
	// limit is the maximum number of matches, 0 for no limit.
	limit int
	// offset is how many matches to skip, 0 for none.
	offset int
	// fuzzy makes string matching case-insensitive and substring-based, which
	// is what PS3.18 8.3.4.3 calls fuzzy matching for PN and the other
	// free-text VRs.
	fuzzy bool
	// includeAll returns the stored dataset as it is instead of the level's
	// projection.
	includeAll bool
	// include names further attributes for the projection.
	include []godicom.Tag
}

// parseQIDOOptions reads the parameters of a Search request.
//
// limit and offset are refused rather than clamped when they are not numbers:
// answering a requestor that asked for "limit=ten" with ten results would be a
// coincidence, and answering with everything would be a superset it cannot
// detect.
func parseQIDOOptions(query url.Values) (qidoOptions, error) {
	var o qidoOptions
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return o, fmt.Errorf("%w: limit=%q", ErrBadQuery, v)
		}
		o.limit = n
	}
	if v := query.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return o, fmt.Errorf("%w: offset=%q", ErrBadQuery, v)
		}
		o.offset = n
	}
	// includefield may repeat and may be a comma-separated list; both forms
	// appear in the wild and the standard allows either.
	for _, raw := range query["includefield"] {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if strings.EqualFold(name, "all") {
				o.includeAll = true
				continue
			}
			tag, err := godicom.ParseTag(name)
			if err != nil {
				return o, fmt.Errorf("%w: includefield=%q", ErrBadQuery, name)
			}
			o.include = append(o.include, tag)
		}
	}
	if v := query.Get("fuzzymatching"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return o, fmt.Errorf("%w: fuzzymatching=%q", ErrBadQuery, v)
		}
		o.fuzzy = b
	}
	return o, nil
}

// page applies limit and offset to an ordered result set.
//
// The order matters and is the caller's: offset is only a number if the answer
// does not reshuffle between requests, which is why the searches sort.
func page(matches []*godicom.Dataset, o qidoOptions) []*godicom.Dataset {
	if o.offset > 0 {
		if o.offset >= len(matches) {
			return nil
		}
		matches = matches[o.offset:]
	}
	if o.limit > 0 && o.limit < len(matches) {
		matches = matches[:o.limit]
	}
	return matches
}

// project returns what the response carries for one match: the level's own
// attributes plus whatever includefield asked for, or the whole dataset when it
// asked for all.
func project(meta *godicom.Dataset, o qidoOptions, defaults ...string) *godicom.Dataset {
	if o.includeAll {
		return meta.Clone()
	}
	if len(o.include) == 0 {
		return projectMeta(meta, defaults...)
	}
	ds := projectMeta(meta, defaults...)
	for _, tag := range o.include {
		if elem, ok := meta.Get(tag); ok {
			ds.Set(elem)
		}
	}
	return ds
}

// fuzzyVR reports whether fuzzy matching applies to a VR: the free-text ones,
// where a substring is a meaningful answer. A date, a number or a UID is
// matched exactly even when fuzzymatching is on — "2024" is not a fuzzy 20240101.
func fuzzyVR(vr godicom.VR) bool {
	switch vr {
	case godicom.VRPN, godicom.VRLO, godicom.VRSH, godicom.VRST, godicom.VRLT,
		godicom.VRUT, godicom.VRUC, godicom.VRCS, godicom.VRAE:
		return true
	default:
		return false
	}
}
