# Panel driver (udl) on turing01-04

The rack panel is a DisplayLink **UV01DA** (`17e9:039a`), a DL-1x5-class USB 2.0
chip. It is driven by the kernel's `udl` DRM driver, **not** DisplayLink's
`evdi`/DisplayLinkManager package (that only supports DL-3xxx and newer; its
udev rule never matches this device, so the service never starts).

The Ubuntu rockchip kernel (`6.1.0-1025-rockchip`, 6.1.75) is built with
`CONFIG_DRM_UDL` unset, so `udl` is built out of tree from the matching
kernel.org sources.

## Build (already done once in `~/udl-build`)

```bash
mkdir -p ~/udl-build && cd ~/udl-build
base="https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/plain/drivers/gpu/drm/udl"
curl -fsSL "$base/Makefile?h=v6.1.75" | sed 's/$(CONFIG_DRM_UDL)/m/' > Kbuild
for f in udl_connector.c udl_connector.h udl_drv.c udl_drv.h udl_main.c udl_modeset.c udl_transfer.c; do
  curl -fsSL -o "$f" "$base/$f?h=v6.1.75"
done
make -C /lib/modules/$(uname -r)/build M=$PWD modules
sudo modprobe drm_shmem_helper && sudo insmod udl.ko
```

## Make it persistent (DKMS, survives reboots and kernel upgrades)

```bash
sudo mkdir -p /usr/src/udl-6.1
sudo cp ~/udl-build/*.c ~/udl-build/*.h ~/udl-build/Kbuild /usr/src/udl-6.1/
sudo cp dkms.conf /usr/src/udl-6.1/          # from deploy/udl-dkms/ in this repo
sudo dkms add udl/6.1 && sudo dkms install udl/6.1
echo udl | sudo tee /etc/modules-load.d/udl.conf
```

Then optionally remove the unused DisplayLink packages:
`sudo apt remove displaylink-driver evdi`.

## Driver quirk

`udl` enforces a 640x480 minimum framebuffer while the panel's only mode is
1440x240, so `ADDFB` of a 1440x240 buffer fails with `EINVAL`.
`internal/drm` allocates `max(mode, min)` (1440x480), scans out the top
1440x240, and flushes only that region with `DIRTYFB`.

## Checking

```bash
ls /sys/class/drm/            # expect cardN-VGA-1
cat /sys/class/drm/card*-VGA-1/status /sys/class/drm/card*-VGA-1/modes   # connected / 1440x240
~/displaytest                 # test pattern (stop the pod first: one DRM master at a time)
```
