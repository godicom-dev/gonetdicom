package dicomweb

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/pixels"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/godicom/uid"
)

const (
	MediaTypeJPEG        = "image/jpeg"
	MediaTypePNG         = "image/png"
	MediaTypeOctetStream = "application/octet-stream"
)

// RenderOptions controls WADO-RS Retrieve Rendered encoding.
type RenderOptions struct {
	MediaType string // image/jpeg (default) or image/png
	Quality   int    // JPEG quality 1–100; 0 → 90
	Frame     int    // 1-based frame index; 0/1 → first frame
}

func (o RenderOptions) withDefaults() RenderOptions {
	if o.MediaType == "" {
		o.MediaType = MediaTypeJPEG
	}
	o.MediaType = strings.ToLower(strings.TrimSpace(o.MediaType))
	if o.Quality <= 0 {
		o.Quality = 90
	}
	if o.Quality > 100 {
		o.Quality = 100
	}
	if o.Frame <= 0 {
		o.Frame = 1
	}
	return o
}

// RenderInstance encodes a Part 10 instance as a rendered image (PNG or JPEG).
func RenderInstance(part10 []byte, opts RenderOptions) (mediaType string, body []byte, err error) {
	opts = opts.withDefaults()
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return "", nil, fmt.Errorf("dicomweb: render read: %w", err)
	}
	img, err := renderFileDataset(fd, opts.Frame)
	if err != nil {
		return "", nil, err
	}
	var buf bytes.Buffer
	switch opts.MediaType {
	case MediaTypePNG:
		if err := png.Encode(&buf, img); err != nil {
			return "", nil, err
		}
		return MediaTypePNG, buf.Bytes(), nil
	case MediaTypeJPEG, "image/jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.Quality}); err != nil {
			return "", nil, err
		}
		return MediaTypeJPEG, buf.Bytes(), nil
	default:
		return "", nil, fmt.Errorf("dicomweb: unsupported rendered media type %q", opts.MediaType)
	}
}

// ExtractPixelBulkData returns the Pixel Data value bytes from a Part 10 instance.
func ExtractPixelBulkData(part10 []byte) ([]byte, error) {
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return nil, fmt.Errorf("dicomweb: bulkdata read: %w", err)
	}
	raw, ok := fd.GetBytes(tag.PixelData)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("dicomweb: Pixel Data missing")
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

func renderFileDataset(fd *godicom.FileDataset, frame int) (image.Image, error) {
	desc, err := pixels.DescriptorFromFile(fd)
	if err != nil {
		return nil, fmt.Errorf("dicomweb: pixel descriptor: %w", err)
	}
	if desc.Rows <= 0 || desc.Columns <= 0 {
		return nil, fmt.Errorf("dicomweb: invalid Rows/Columns")
	}
	frames, err := fd.PixelFrames()
	if err != nil {
		return nil, fmt.Errorf("dicomweb: decode pixels: %w", err)
	}
	if frame > len(frames) {
		return nil, fmt.Errorf("dicomweb: frame %d out of range (have %d)", frame, len(frames))
	}
	frameBytes := frames[frame-1]

	samples, err := pixels.UnpackSamples(frameBytes, desc.BitsAllocated, desc.PixelRepresentation, pixelLittleEndian(fd))
	if err != nil {
		return nil, err
	}

	pi := strings.ToUpper(strings.TrimSpace(desc.PhotometricInterpretation))
	switch {
	case strings.HasPrefix(pi, "MONOCHROME"):
		samples, err = applyDisplayPipeline(fd, samples)
		if err != nil {
			return nil, err
		}
		samples = normalize01(samples)
		if pi == "MONOCHROME1" {
			for i, v := range samples {
				samples[i] = 1 - v
			}
		}
		gray := image.NewGray(image.Rect(0, 0, desc.Columns, desc.Rows))
		for y := 0; y < desc.Rows; y++ {
			for x := 0; x < desc.Columns; x++ {
				i := y*desc.Columns + x
				gray.SetGray(x, y, color.Gray{Y: uint8(math.Round(samples[i] * 255))})
			}
		}
		return gray, nil
	case strings.HasPrefix(pi, "RGB"), pi == "YBR_FULL", pi == "YBR_FULL_422":
		if desc.SamplesPerPixel < 3 {
			return nil, fmt.Errorf("dicomweb: color image needs 3 samples/pixel")
		}
		n := desc.Rows * desc.Columns
		if len(samples) < n*3 {
			return nil, fmt.Errorf("dicomweb: insufficient color samples")
		}
		rCh := make([]float64, n)
		gCh := make([]float64, n)
		bCh := make([]float64, n)
		if desc.PlanarConfiguration == 1 {
			copy(rCh, samples[0:n])
			copy(gCh, samples[n:2*n])
			copy(bCh, samples[2*n:3*n])
		} else {
			for i := 0; i < n; i++ {
				rCh[i] = samples[i*3]
				gCh[i] = samples[i*3+1]
				bCh[i] = samples[i*3+2]
			}
		}
		rCh = normalize01(rCh)
		gCh = normalize01(gCh)
		bCh = normalize01(bCh)
		rgba := image.NewRGBA(image.Rect(0, 0, desc.Columns, desc.Rows))
		for y := 0; y < desc.Rows; y++ {
			for x := 0; x < desc.Columns; x++ {
				i := y*desc.Columns + x
				rgba.SetRGBA(x, y, color.RGBA{
					R: uint8(math.Round(rCh[i] * 255)),
					G: uint8(math.Round(gCh[i] * 255)),
					B: uint8(math.Round(bCh[i] * 255)),
					A: 255,
				})
			}
		}
		return rgba, nil
	default:
		return nil, fmt.Errorf("dicomweb: unsupported PhotometricInterpretation %q", desc.PhotometricInterpretation)
	}
}

func applyDisplayPipeline(fd *godicom.FileDataset, samples []float64) ([]float64, error) {
	out, err := fd.ApplyModalityLUT(samples)
	if err != nil {
		return nil, err
	}
	out, err = fd.ApplyVOILUT(out, 0, true)
	if err != nil {
		return nil, err
	}
	return fd.ApplyPresentationLUTShape(out)
}

func normalize01(arr []float64) []float64 {
	if len(arr) == 0 {
		return arr
	}
	minV, maxV := arr[0], arr[0]
	for _, v := range arr[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	out := make([]float64, len(arr))
	if maxV == minV {
		for i := range out {
			out[i] = 0
		}
		return out
	}
	span := maxV - minV
	for i, v := range arr {
		out[i] = (v - minV) / span
	}
	return out
}

func pixelLittleEndian(fd *godicom.FileDataset) bool {
	ts, ok := fd.TransferSyntaxUID()
	if !ok || ts == "" {
		return true
	}
	return uid.UID(ts).IsLittleEndian()
}

func parseRenderOptions(accept string, query map[string][]string) RenderOptions {
	opts := RenderOptions{}
	al := strings.ToLower(accept)
	switch {
	case strings.Contains(al, MediaTypePNG):
		opts.MediaType = MediaTypePNG
	case strings.Contains(al, MediaTypeJPEG), strings.Contains(al, "image/jpg"):
		opts.MediaType = MediaTypeJPEG
	default:
		opts.MediaType = MediaTypeJPEG
	}
	if q := firstQuery(query, "quality"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			opts.Quality = n
		}
	}
	if q := firstQuery(query, "frame"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			opts.Frame = n
		}
	}
	return opts
}

func firstQuery(query map[string][]string, key string) string {
	if query == nil {
		return ""
	}
	vals := query[key]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}
