package dicomweb

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/encaps"
	"github.com/godicom-dev/godicom/pixels"
	"github.com/godicom-dev/godicom/tag"
)

var errNoPixelData = errors.New("dicomweb: Pixel Data missing")

// ExtractFrames returns the transfer syntax UID and one stored frame per
// logical frame. Uncompressed frames are the raw pixel bytes. Compressed
// frames are the encapsulated bitstream with fragment headers removed, not
// decoded pixels: WADO-RS must return the encoding the metadata names.
func ExtractFrames(part10 []byte) (transferSyntax string, frames [][]byte, err error) {
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return "", nil, fmt.Errorf("dicomweb: frames read: %w", err)
	}
	desc, err := pixels.DescriptorFromFile(fd)
	if err != nil {
		return "", nil, fmt.Errorf("dicomweb: frames: %w", err)
	}
	pixelData, ok := fd.GetBytes(tag.PixelData)
	if !ok || len(pixelData) == 0 {
		return "", nil, errNoPixelData
	}
	ts := string(desc.TransferSyntaxUID)
	if !desc.TransferSyntaxUID.IsCompressed() {
		frames, err = nativeWireFrames(pixelData, desc)
		if err != nil {
			return "", nil, err
		}
		return ts, frames, nil
	}
	encOpts := encaps.FramesOptions{
		NumberOfFrames: desc.NumberOfFrames,
		LittleEndian:   true,
	}
	if desc.ExtendedOffsets != nil {
		encOpts.ExtendedOffsets = &encaps.ExtendedOffsetTable{
			Offsets: desc.ExtendedOffsets.Offsets,
			Lengths: desc.ExtendedOffsets.Lengths,
		}
	}
	frames, err = encaps.GenerateFrames(pixelData, encOpts)
	if err != nil {
		return "", nil, fmt.Errorf("dicomweb: frames: %w", err)
	}
	return ts, frames, nil
}

func nativeWireFrames(pixelData []byte, desc pixels.Descriptor) ([][]byte, error) {
	frameBytes := desc.UnpackedFrameBytes()
	if desc.PhotometricInterpretation == "YBR_FULL_422" {
		nBytes := desc.BitsAllocated / 8
		if nBytes < 1 {
			return nil, fmt.Errorf("dicomweb: invalid BitsAllocated %d for YBR_FULL_422", desc.BitsAllocated)
		}
		frameBytes = desc.Rows * desc.Columns * 2 * nBytes
	}
	if frameBytes < 1 || desc.NumberOfFrames < 1 {
		return nil, fmt.Errorf("dicomweb: invalid frame size")
	}
	want := frameBytes * desc.NumberOfFrames
	// A defined-length OB value is padded to an even length.
	if len(pixelData) == want+1 && pixelData[len(pixelData)-1] == 0 {
		pixelData = pixelData[:want]
	}
	if len(pixelData) != want {
		return nil, fmt.Errorf("dicomweb: native pixel data length %d, want %d", len(pixelData), want)
	}
	out := make([][]byte, desc.NumberOfFrames)
	for i := 0; i < desc.NumberOfFrames; i++ {
		start := i * frameBytes
		out[i] = append([]byte(nil), pixelData[start:start+frameBytes]...)
	}
	return out, nil
}

// parseFrameList reads the PS3.18 frame list: 1-based numbers separated by commas.
func parseFrameList(list string) ([]int, error) {
	if list == "" {
		return nil, fmt.Errorf("dicomweb: empty frame list")
	}
	parts := strings.Split(list, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("dicomweb: bad frame number %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}
