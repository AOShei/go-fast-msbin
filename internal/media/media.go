// Package media makes the image blocks of pictures the three formats keep
// through the drawing layer.
package media

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif" // sizes of pictures are read from their headers
	_ "image/jpeg"
	_ "image/png"
	"strconv"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/officeart"
)

// ErrBudget says the limit on decoded bytes was reached while a picture
// the file keeps compressed was expanded.
var ErrBudget = errors.New("the limit on decoded bytes was reached")

// Image describes the picture with the given number, from 1, of the store
// of pictures: its name, which is its number and the extension of its
// type; its type; its size in pixels where the format is one the standard
// library can read a header of; and with ImagesData its bytes, as a file
// of its type holds them. A picture the file keeps compressed is
// expanded only when its bytes are asked for, and what it expands to is
// taken from decoded. The error is ErrBudget if that ran out, and nil
// otherwise: a picture that cannot be read is still named.
func Image(number int, blip officeart.Rec, mode gofastcore.ImageMode, decoded *gofastcore.DecodeBudget) (*gofastcore.Image, error) {
	img := &gofastcore.Image{ID: "image" + strconv.Itoa(number)}
	if blip.Type == 0 {
		return img, nil
	}
	left := int64(1) << 40
	if decoded != nil {
		left = decoded.Left()
	}
	// A metafile is expanded only for its bytes: its size in pixels is
	// not in a header the standard library reads.
	pic, err := officeart.Blip(blip, false, left)
	metafile := pic.Ext == "emf" || pic.Ext == "wmf" || pic.Ext == "pict"
	if err == nil && (mode == gofastcore.ImagesData || !metafile) {
		pic, err = officeart.Blip(blip, true, left)
	}
	img.MIME = pic.MIME
	if pic.Ext != "" {
		img.ID += "." + pic.Ext
	}
	if errors.Is(err, officeart.ErrBudget) {
		return img, ErrBudget
	}
	if err != nil || pic.Data == nil {
		return img, nil
	}
	if pic.Compressed && !decoded.Take(int64(len(pic.Data))) {
		return img, ErrBudget
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(pic.Data[:min(len(pic.Data), 1<<20)])); err == nil {
		img.Width, img.Height = cfg.Width, cfg.Height
	}
	if mode == gofastcore.ImagesData {
		img.Data = pic.Data
	}
	return img, nil
}
