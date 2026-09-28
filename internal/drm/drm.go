// Package drm drives a display through Linux DRM/KMS using a single "dumb"
// buffer. It needs no X11/Wayland/compositor, which makes it suitable for a
// headless node (and later a privileged pod with /dev/dri mounted).
//
// Designed around USB displays such as DisplayLink (udl/evdi), which do not
// scan out continuously: after drawing we issue DIRTYFB so the driver pushes
// the changed pixels over USB.
package drm

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ---- ioctl plumbing (include/uapi/drm/drm.h, drm_mode.h) ----

const (
	iocNone  = 0
	iocWrite = 1
	iocRead  = 2
)

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | uintptr('d')<<8 | nr }
func iowr(nr, size uintptr) uintptr     { return ioc(iocRead|iocWrite, nr, size) }

type cardRes struct {
	FbIDPtr, CrtcIDPtr, ConnectorIDPtr, EncoderIDPtr     uint64
	CountFbs, CountCrtcs, CountConnectors, CountEncoders uint32
	MinWidth, MaxWidth, MinHeight, MaxHeight             uint32
}

type ModeInfo struct {
	Clock                                         uint32
	Hdisplay, HsyncStart, HsyncEnd, Htotal, Hskew uint16
	Vdisplay, VsyncStart, VsyncEnd, Vtotal, Vscan uint16
	Vrefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

func (m ModeInfo) String() string {
	return fmt.Sprintf("%dx%d@%d", m.Hdisplay, m.Vdisplay, m.Vrefresh)
}

type getConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr uint64
	CountModes, CountProps, CountEncoders          uint32
	EncoderID, ConnectorID                         uint32
	ConnectorType, ConnectorTypeID                 uint32
	Connection, MmWidth, MmHeight, Subpixel, Pad   uint32
}

type getEncoder struct {
	EncoderID, EncoderType, CrtcID, PossibleCrtcs, PossibleClones uint32
}

type modeCrtc struct {
	SetConnectorsPtr                 uint64
	CountConnectors, CrtcID, FbID, X uint32
	Y, GammaSize, ModeValid          uint32
	Mode                             ModeInfo
}

type createDumb struct {
	Height, Width, Bpp, Flags, Handle, Pitch uint32
	Size                                     uint64
}

type mapDumb struct {
	Handle, Pad uint32
	Offset      uint64
}

type destroyDumb struct{ Handle uint32 }

type fbCmd struct {
	FbID, Width, Height, Pitch, Bpp, Depth, Handle uint32
}

type clipRect struct{ X1, Y1, X2, Y2 uint16 }

type fbDirtyCmd struct {
	FbID, Flags, Color, NumClips uint32
	ClipsPtr                     uint64
}

var (
	ioctlSetMaster    = ioc(iocNone, 0x1e, 0)
	ioctlDropMaster   = ioc(iocNone, 0x1f, 0)
	ioctlGetResources = iowr(0xA0, unsafe.Sizeof(cardRes{}))
	ioctlSetCrtc      = iowr(0xA2, unsafe.Sizeof(modeCrtc{}))
	ioctlGetEncoder   = iowr(0xA6, unsafe.Sizeof(getEncoder{}))
	ioctlGetConnector = iowr(0xA7, unsafe.Sizeof(getConnector{}))
	ioctlAddFB        = iowr(0xAE, unsafe.Sizeof(fbCmd{}))
	ioctlRmFB         = iowr(0xAF, unsafe.Sizeof(uint32(0)))
	ioctlDirtyFB      = iowr(0xB1, unsafe.Sizeof(fbDirtyCmd{}))
	ioctlCreateDumb   = iowr(0xB2, unsafe.Sizeof(createDumb{}))
	ioctlMapDumb      = iowr(0xB3, unsafe.Sizeof(mapDumb{}))
	ioctlDestroyDumb  = iowr(0xB4, unsafe.Sizeof(destroyDumb{}))
)

const (
	connected         = 1
	modeTypePreferred = 1 << 3
)

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		switch errno {
		case 0:
			return nil
		case unix.EINTR, unix.EAGAIN:
			continue
		default:
			return errno
		}
	}
}

func ptr[T any](s []T) uint64 {
	if len(s) == 0 {
		return 0
	}
	return uint64(uintptr(unsafe.Pointer(&s[0])))
}

// ---- card discovery ----

// Card describes a /dev/dri/cardN node and the kernel driver behind it.
type Card struct {
	Path   string
	Driver string
}

// ListCards returns all DRM primary nodes with their driver names.
func ListCards() ([]Card, error) {
	paths, err := filepath.Glob("/dev/dri/card*")
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var cards []Card
	for _, p := range paths {
		name := filepath.Base(p)
		link, _ := filepath.EvalSymlinks(filepath.Join("/sys/class/drm", name, "device/driver"))
		cards = append(cards, Card{Path: p, Driver: filepath.Base(link)})
	}
	return cards, nil
}

// FindCard picks a card by driver name preference (e.g. "udl", "evdi").
// The first card with a matching driver and a connected connector wins.
func FindCard(drivers ...string) (string, error) {
	cards, err := ListCards()
	if err != nil {
		return "", err
	}
	for _, want := range drivers {
		for _, c := range cards {
			if c.Driver != want {
				continue
			}
			if hasConnectedConnector(c.Path) {
				return c.Path, nil
			}
		}
	}
	var seen []string
	for _, c := range cards {
		seen = append(seen, fmt.Sprintf("%s(%s)", c.Path, c.Driver))
	}
	return "", fmt.Errorf("no connected card with driver in %v; found: %s", drivers, strings.Join(seen, " "))
}

func hasConnectedConnector(path string) bool {
	matches, _ := filepath.Glob(filepath.Join("/sys/class/drm", filepath.Base(path)+"-*", "status"))
	for _, m := range matches {
		b, _ := os.ReadFile(m)
		if strings.TrimSpace(string(b)) == "connected" {
			return true
		}
	}
	return false
}

// ---- display ----

// Display is a mode-set connector with an XRGB8888 dumb framebuffer.
type Display struct {
	f           *os.File
	fd          int
	Mode        ModeInfo
	ConnectorID uint32
	crtcID      uint32
	fbID        uint32
	fbW, fbH    uint32
	handle      uint32
	pitch       uint32
	mem         []byte
}

// Options control mode selection.
type Options struct {
	Width, Height int // optional; 0 = preferred mode
}

// Open mode-sets the first connected connector on the card at path.
func Open(path string, opt Options) (*Display, error) {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := &Display{f: f, fd: int(f.Fd())}
	if err := d.init(opt); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Display) init(opt Options) error {
	// Becoming master is required for SETCRTC; ignore failure (we may already be
	// master as the first opener) and let SETCRTC report the real error.
	_ = ioctl(d.fd, ioctlSetMaster, nil)

	var res cardRes
	if err := ioctl(d.fd, ioctlGetResources, unsafe.Pointer(&res)); err != nil {
		return fmt.Errorf("GETRESOURCES: %w", err)
	}
	crtcs := make([]uint32, res.CountCrtcs)
	conns := make([]uint32, res.CountConnectors)
	encs := make([]uint32, res.CountEncoders)
	fbs := make([]uint32, res.CountFbs)
	res.CrtcIDPtr, res.ConnectorIDPtr, res.EncoderIDPtr, res.FbIDPtr = ptr(crtcs), ptr(conns), ptr(encs), ptr(fbs)
	if err := ioctl(d.fd, ioctlGetResources, unsafe.Pointer(&res)); err != nil {
		return fmt.Errorf("GETRESOURCES: %w", err)
	}
	runtime.KeepAlive(fbs)

	for _, cid := range conns {
		conn, modes, connEncs, err := d.connector(cid)
		if err != nil {
			return err
		}
		if conn.Connection != connected || len(modes) == 0 {
			continue
		}
		mode, err := pickMode(modes, opt)
		if err != nil {
			return err
		}
		crtc, err := d.findCrtc(conn, connEncs, crtcs)
		if err != nil {
			return err
		}
		d.Mode, d.ConnectorID, d.crtcID = mode, cid, crtc
		// Some drivers enforce a minimum framebuffer size larger than the mode
		// (udl: 640x480, while our panel is 1440x240). Allocate at least the
		// minimum and scan out the top-left mode-sized region.
		d.fbW = max(uint32(mode.Hdisplay), res.MinWidth)
		d.fbH = max(uint32(mode.Vdisplay), res.MinHeight)
		return d.setup()
	}
	return errors.New("no connected connector with modes")
}

func (d *Display) connector(id uint32) (getConnector, []ModeInfo, []uint32, error) {
	// First call probes the connector (reads EDID) and returns counts.
	c := getConnector{ConnectorID: id}
	if err := ioctl(d.fd, ioctlGetConnector, unsafe.Pointer(&c)); err != nil {
		return c, nil, nil, fmt.Errorf("GETCONNECTOR %d: %w", id, err)
	}
	modes := make([]ModeInfo, c.CountModes)
	encs := make([]uint32, c.CountEncoders)
	props := make([]uint32, c.CountProps)
	vals := make([]uint64, c.CountProps)
	c.ModesPtr, c.EncodersPtr, c.PropsPtr, c.PropValuesPtr = ptr(modes), ptr(encs), ptr(props), ptr(vals)
	if err := ioctl(d.fd, ioctlGetConnector, unsafe.Pointer(&c)); err != nil {
		return c, nil, nil, fmt.Errorf("GETCONNECTOR %d: %w", id, err)
	}
	runtime.KeepAlive(props)
	runtime.KeepAlive(vals)
	return c, modes[:c.CountModes], encs[:c.CountEncoders], nil
}

func pickMode(modes []ModeInfo, opt Options) (ModeInfo, error) {
	if opt.Width > 0 && opt.Height > 0 {
		for _, m := range modes {
			if int(m.Hdisplay) == opt.Width && int(m.Vdisplay) == opt.Height {
				return m, nil
			}
		}
		var avail []string
		for _, m := range modes {
			avail = append(avail, m.String())
		}
		return ModeInfo{}, fmt.Errorf("mode %dx%d not offered; available: %s", opt.Width, opt.Height, strings.Join(avail, " "))
	}
	for _, m := range modes {
		if m.Type&modeTypePreferred != 0 {
			return m, nil
		}
	}
	return modes[0], nil
}

func (d *Display) findCrtc(conn getConnector, encs, crtcs []uint32) (uint32, error) {
	if conn.EncoderID != 0 {
		e := getEncoder{EncoderID: conn.EncoderID}
		if ioctl(d.fd, ioctlGetEncoder, unsafe.Pointer(&e)) == nil && e.CrtcID != 0 {
			return e.CrtcID, nil
		}
	}
	for _, eid := range encs {
		e := getEncoder{EncoderID: eid}
		if err := ioctl(d.fd, ioctlGetEncoder, unsafe.Pointer(&e)); err != nil {
			continue
		}
		for i, c := range crtcs {
			if e.PossibleCrtcs&(1<<i) != 0 {
				return c, nil
			}
		}
	}
	return 0, errors.New("no usable CRTC for connector")
}

func (d *Display) setup() error {
	w, h := d.fbW, d.fbH
	cd := createDumb{Width: w, Height: h, Bpp: 32}
	if err := ioctl(d.fd, ioctlCreateDumb, unsafe.Pointer(&cd)); err != nil {
		return fmt.Errorf("CREATE_DUMB: %w", err)
	}
	d.handle, d.pitch = cd.Handle, cd.Pitch

	fb := fbCmd{Width: w, Height: h, Pitch: cd.Pitch, Bpp: 32, Depth: 24, Handle: cd.Handle}
	if err := ioctl(d.fd, ioctlAddFB, unsafe.Pointer(&fb)); err != nil {
		return fmt.Errorf("ADDFB: %w", err)
	}
	d.fbID = fb.FbID

	md := mapDumb{Handle: cd.Handle}
	if err := ioctl(d.fd, ioctlMapDumb, unsafe.Pointer(&md)); err != nil {
		return fmt.Errorf("MAP_DUMB: %w", err)
	}
	mem, err := unix.Mmap(d.fd, int64(md.Offset), int(cd.Size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}
	d.mem = mem
	clear(d.mem)

	conns := []uint32{d.ConnectorID}
	sc := modeCrtc{
		SetConnectorsPtr: ptr(conns), CountConnectors: 1,
		CrtcID: d.crtcID, FbID: d.fbID, ModeValid: 1, Mode: d.Mode,
	}
	if err := ioctl(d.fd, ioctlSetCrtc, unsafe.Pointer(&sc)); err != nil {
		return fmt.Errorf("SETCRTC (is another process DRM master on this card?): %w", err)
	}
	runtime.KeepAlive(conns)
	return nil
}

// Bounds returns the active display area.
func (d *Display) Bounds() image.Rectangle {
	return image.Rect(0, 0, int(d.Mode.Hdisplay), int(d.Mode.Vdisplay))
}

// Show copies img into the framebuffer (XRGB8888) and flushes it to the device.
func (d *Display) Show(img *image.RGBA) error {
	b := d.Bounds().Intersect(img.Bounds())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		src := img.Pix[img.PixOffset(b.Min.X, y):]
		dst := d.mem[y*int(d.pitch)+b.Min.X*4:]
		for x := 0; x < b.Dx(); x++ {
			s, t := src[x*4:x*4+4:x*4+4], dst[x*4:x*4+4:x*4+4]
			t[0], t[1], t[2], t[3] = s[2], s[1], s[0], 0xff
		}
	}
	// Flush only the visible region (the framebuffer may be larger than the
	// mode). Drivers that scan out continuously return ENOSYS, which is fine.
	clips := []clipRect{{X2: d.Mode.Hdisplay, Y2: d.Mode.Vdisplay}}
	dc := fbDirtyCmd{FbID: d.fbID, NumClips: 1, ClipsPtr: ptr(clips)}
	err := ioctl(d.fd, ioctlDirtyFB, unsafe.Pointer(&dc))
	runtime.KeepAlive(clips)
	if err != nil && !errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("DIRTYFB: %w", err)
	}
	return nil
}

// Close releases the framebuffer and DRM master.
func (d *Display) Close() error {
	if d.mem != nil {
		_ = unix.Munmap(d.mem)
	}
	if d.fbID != 0 {
		id := d.fbID
		_ = ioctl(d.fd, ioctlRmFB, unsafe.Pointer(&id))
	}
	if d.handle != 0 {
		dd := destroyDumb{Handle: d.handle}
		_ = ioctl(d.fd, ioctlDestroyDumb, unsafe.Pointer(&dd))
	}
	_ = ioctl(d.fd, ioctlDropMaster, nil)
	return d.f.Close()
}
