// Package fbdev is a fallback output that writes to a legacy /dev/fbN device
// (e.g. DRM fbdev emulation). Geometry is read from sysfs to avoid the
// fb_var_screeninfo ioctl dance.
package fbdev

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Framebuffer is an mmapped /dev/fbN.
type Framebuffer struct {
	f             *os.File
	mem           []byte
	width, height int
	stride, bpp   int
}

// Open maps the framebuffer device at path (e.g. /dev/fb1).
func Open(path string) (*Framebuffer, error) {
	sys := filepath.Join("/sys/class/graphics", filepath.Base(path))
	size, err := readSys(sys, "virtual_size")
	if err != nil {
		return nil, err
	}
	w, h, ok := strings.Cut(size, ",")
	if !ok {
		return nil, fmt.Errorf("unexpected virtual_size %q", size)
	}
	fb := &Framebuffer{}
	fb.width, _ = strconv.Atoi(w)
	fb.height, _ = strconv.Atoi(h)
	if s, err := readSys(sys, "stride"); err == nil {
		fb.stride, _ = strconv.Atoi(s)
	}
	if s, err := readSys(sys, "bits_per_pixel"); err == nil {
		fb.bpp, _ = strconv.Atoi(s)
	}
	if fb.bpp != 16 && fb.bpp != 32 {
		return nil, fmt.Errorf("unsupported bits_per_pixel %d", fb.bpp)
	}
	if fb.stride == 0 {
		fb.stride = fb.width * fb.bpp / 8
	}

	fb.f, err = os.OpenFile(path, os.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	fb.mem, err = unix.Mmap(int(fb.f.Fd()), 0, fb.stride*fb.height, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		fb.f.Close()
		return nil, fmt.Errorf("mmap: %w", err)
	}
	return fb, nil
}

func readSys(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b)), err
}

// Bounds returns the framebuffer size.
func (fb *Framebuffer) Bounds() image.Rectangle { return image.Rect(0, 0, fb.width, fb.height) }

// Show writes img into the framebuffer as XRGB8888 or RGB565.
func (fb *Framebuffer) Show(img *image.RGBA) error {
	b := fb.Bounds().Intersect(img.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		src := img.Pix[img.PixOffset(b.Min.X, y):]
		row := fb.mem[y*fb.stride:]
		for x := 0; x < b.Dx(); x++ {
			r, g, bl := src[x*4], src[x*4+1], src[x*4+2]
			if fb.bpp == 32 {
				row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = bl, g, r, 0xff
			} else {
				v := uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(bl>>3)
				row[x*2], row[x*2+1] = byte(v), byte(v>>8)
			}
		}
	}
	return nil
}

// Close unmaps the framebuffer.
func (fb *Framebuffer) Close() error {
	_ = unix.Munmap(fb.mem)
	return fb.f.Close()
}
