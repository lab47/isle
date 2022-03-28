
if [ "$(uname -p)" = "aarch64" ]; then
  kernel=https://github.com/lab47/yalr4m-kernel/releases/download/v20220327/linux-5.16.17-arm64.tar.xz
  root=https://dl-cdn.alpinelinux.org/alpine/v3.15/releases/aarch64/alpine-minirootfs-3.15.2-aarch64.tar.gz
  initrd=https://github.com/lab47/yalr4m-kernel/releases/download/v20220327/initrd-aarch64
else
  kernel=https://github.com/lab47/yalr4m-kernel/releases/download/v20220327/linux-5.16.17-x86.tar.xz
  root=https://dl-cdn.alpinelinux.org/alpine/v3.15/releases/x86_64/alpine-minirootfs-3.15.2-x86_64.tar.gz
  initrd=https://github.com/lab47/yalr4m-kernel/releases/download/v20220327/initrd-x86_64
fi

rm -rf rootfs
mkdir -p rootfs

mkdir -p release

echo "+ Downloading assets"

if ! test -e release/initrd; then
  curl -o release/initrd -L $initrd
fi

if ! test -e rootfs.tar.gz; then
  curl -o rootfs.tar.gz -L $root
fi

if ! test -e kernel.tar.xz; then
  curl -o kernel.tar.xz -L $kernel
fi

echo "+ Unpacking rootfs"
pushd rootfs
tar xf ../rootfs.tar.gz
popd

echo "+ Applying custom code"
cp -a custom/* rootfs

chown root.root -R rootfs

echo "+ Add package"

cp /etc/resolv.conf rootfs/etc/resolv.conf

chroot rootfs apk add --no-cache

rm rootfs/etc/resolv.conf

echo "+ Add macstorage user"

echo "macstorage:x:147:100:For external file access,,,:/tmp:/sbin/nologin" >> rootfs/etc/passwd
echo "macstorage:!::0:::::" >> rootfs/etc/shadow

echo "+ Adding yalr4m-guest"

cp yalr4m-guest rootfs/usr/sbin/

echo "+ Adding kernel models"

tar -C rootfs -xf kernel.tar.xz

gunzip < rootfs/boot/vmlinuz-* > release/vmlinux

# We don't use these and they just take up space.
rm -rf rootfs/boot/vmlinu*

KERNEL_VERSION=$(ls rootfs/lib/modules | head -n 1)

echo "- Running depmod to be sure modules are ready"

chroot rootfs depmod -ae -F /boot/System.map-$KERNEL_VERSION $KERNEL_VERSION

rm release/os.fs
mksquashfs rootfs release/os.fs -comp xz
